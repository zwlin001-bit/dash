package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// DefaultTimeout 默认子进程执行超时时间
	DefaultTimeout = 120 * time.Second

	// OutputTruncateLimit 输出截断阈值（末尾 8000 字节量级）
	OutputTruncateLimit = 8000
)

// RunOptions 定义子进程执行参数
type RunOptions struct {
	Command []string      // 待执行命令 argv（argv[0] 为可执行文件名，argv[1:] 为参数）
	Timeout time.Duration // 超时时间，0 使用默认值
	Cwd     string        // 工作目录，空则沿用当前目录
	RunAs   string        // 降权目标用户；非 root 启动或用户不存在时安全降级不降权
	Env     []string      // 环境变量；若需添加自定义环境变量可传入
}

// RunResult 定义子进程执行结果
type RunResult struct {
	ExitCode  int    // 退出码（超时恒为 124）
	Stdout    string // 标准输出（尾部截断）
	Stderr    string // 标准错误（尾部截断）
	Truncated bool   // 是否发生截断
}

// Elev 要 root 时自适应。★ 只用 -n(非交互): 卡在密码提示上等于这台机器永远不上报。
func Elev(argv []string) []string {
	if os.Geteuid() == 0 {
		return argv
	}
	return append([]string{"sudo", "-n"}, argv...)
}

// Run 执行子进程，严格遵守四大安全与稳定性约束：
// ① 降权走内核 syscall.Credential 完成，并摆正 HOME/USER/LOGNAME；
// ② 目标用户不存在时不报错，按原样执行（避免环境差异导致假故障）；
// ③ 永不 shell=True，一律 argv 数组传递给 execve；
// ④ 输出截断收敛到 8000 字节以内并置 truncated: true；超时返回 exit_code=124。
func Run(ctx context.Context, opt RunOptions) (*RunResult, error) {
	if len(opt.Command) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// ★ 永不 shell：直接使用 argv 数组，不调用 sh -c
	name := opt.Command[0]
	var args []string
	if len(opt.Command) > 1 {
		args = opt.Command[1:]
	}

	cmd := osexec.CommandContext(execCtx, name, args...)
	if opt.Cwd != "" {
		cmd.Dir = opt.Cwd
	}

	// 准备环境变量
	baseEnv := opt.Env
	if len(baseEnv) == 0 {
		baseEnv = os.Environ()
	}
	env := make([]string, len(baseEnv))
	copy(env, baseEnv)

	// ★ 降权逻辑：仅在当前为 root (euid == 0) 且指定了 run_as 时生效
	// 降权完全基于内核能力 syscall.Credential
	if opt.RunAs != "" && os.Geteuid() == 0 {
		cred, pwDir, pwUser, lookupErr := prepareCredential(opt.RunAs)
		if lookupErr == nil && cred != nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Credential: cred,
			}
			// ★ 必须摆正 HOME/USER/LOGNAME
			env = setEnvVar(env, "HOME", pwDir)
			env = setEnvVar(env, "USER", pwUser)
			env = setEnvVar(env, "LOGNAME", pwUser)
		}
		// ★ 目标用户不存在时 lookupErr != nil：按原样执行，不降权、不返回错误
	}

	cmd.Env = env

	stdoutBuf := newTailBuffer(OutputTruncateLimit * 2)
	stderrBuf := newTailBuffer(OutputTruncateLimit * 2)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	err := cmd.Run()

	rawStdout := stdoutBuf.Bytes()
	rawStderr := stderrBuf.Bytes()
	wasTruncated := stdoutBuf.Truncated() || stderrBuf.Truncated()

	// 判断是否超时
	if execCtx.Err() == context.DeadlineExceeded || (ctx.Err() != context.DeadlineExceeded && errors.Is(err, context.DeadlineExceeded)) {
		stdOutStr, stdErrStr, trunc := truncateCombined(rawStdout, rawStderr, OutputTruncateLimit, wasTruncated)
		if stdErrStr == "" {
			stdErrStr = fmt.Sprintf("command timed out after %v", timeout)
		}
		return &RunResult{
			ExitCode:  124, // ★ 超时恒为 124，与老 agent 对齐
			Stdout:    stdOutStr,
			Stderr:    stdErrStr,
			Truncated: trunc,
		}, nil
	}

	exitCode := 0
	if err != nil {
		var exitErr *osexec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			// 其他启动或系统错误，返回 exit_code = 1
			exitCode = 1
			if len(rawStderr) == 0 {
				rawStderr = []byte(err.Error())
			}
		}
	}

	stdOutStr, stdErrStr, trunc := truncateCombined(rawStdout, rawStderr, OutputTruncateLimit, wasTruncated)

	return &RunResult{
		ExitCode:  exitCode,
		Stdout:    stdOutStr,
		Stderr:    stdErrStr,
		Truncated: trunc,
	}, nil
}

// prepareCredential 查询目标用户并构造 syscall.Credential。
// 若用户不存在，返回错误，上层安全降级。
func prepareCredential(username string) (*syscall.Credential, string, string, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return nil, "", "", err
	}

	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, "", "", err
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, "", "", err
	}

	groups := []uint32{uint32(gid)}
	if gids, err := u.GroupIds(); err == nil {
		for _, gStr := range gids {
			if g, err := strconv.ParseUint(gStr, 10, 32); err == nil {
				if uint32(g) != uint32(gid) {
					groups = append(groups, uint32(g))
				}
			}
		}
	}

	return &syscall.Credential{
		Uid:    uint32(uid),
		Gid:    uint32(gid),
		Groups: groups,
	}, u.HomeDir, u.Username, nil
}

func setEnvVar(env []string, key, val string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
}

// truncateCombined 将 stdout 与 stderr 的总和保留在 limit 字节以内（保留末尾），并判断是否截断
func truncateCombined(stdout, stderr []byte, limit int, previouslyTruncated bool) (string, string, bool) {
	totalLen := len(stdout) + len(stderr)
	if totalLen <= limit && !previouslyTruncated {
		return string(stdout), string(stderr), false
	}

	// 发生截断
	if totalLen <= limit {
		return string(stdout), string(stderr), true
	}

	// 需要裁剪使总长度 <= limit
	// 若 stderr 较短，优先保留完整的 stderr，剩余额度分给 stdout 的末尾
	if len(stderr) < limit/2 {
		outQuota := limit - len(stderr)
		if len(stdout) > outQuota {
			stdout = stdout[len(stdout)-outQuota:]
		}
	} else if len(stdout) < limit/2 {
		errQuota := limit - len(stdout)
		if len(stderr) > errQuota {
			stderr = stderr[len(stderr)-errQuota:]
		}
	} else {
		// 双方都较长，各取一半末尾
		half := limit / 2
		if len(stdout) > half {
			stdout = stdout[len(stdout)-half:]
		}
		if len(stderr) > (limit - len(stdout)) {
			stderr = stderr[len(stderr)-(limit-len(stdout)):]
		}
	}

	return string(stdout), string(stderr), true
}

// tailBuffer 维护保留最后 maxCap 字节的有界内存缓冲，防止无限输出导致 OOM
type tailBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	maxCap    int
	truncated bool
}

func newTailBuffer(maxCap int) *tailBuffer {
	if maxCap <= 0 {
		maxCap = OutputTruncateLimit
	}
	return &tailBuffer{
		maxCap: maxCap,
	}
}

func (b *tailBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n = len(p)
	if n == 0 {
		return 0, nil
	}

	b.buf.Write(p)
	if b.buf.Len() > b.maxCap {
		b.truncated = true
		excess := b.buf.Len() - b.maxCap
		trimmed := b.buf.Bytes()[excess:]
		newBuf := make([]byte, len(trimmed))
		copy(newBuf, trimmed)
		b.buf.Reset()
		b.buf.Write(newBuf)
	}

	return n, nil
}

func (b *tailBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	res := make([]byte, b.buf.Len())
	copy(res, b.buf.Bytes())
	return res
}

func (b *tailBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

var _ io.Writer = (*tailBuffer)(nil)
