package db

import (
	"testing"
)

func TestCheckSafeDB(t *testing.T) {
	tests := []struct {
		name    string
		driver  string
		user    string
		dsn     string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "safe mysql config",
			driver:  "mysql",
			user:    "dash_user",
			dsn:     "dash_user:secret@tcp(127.0.0.1:3306)/dash?parseTime=true",
			wantErr: false,
		},
		{
			name:    "safe oracle config",
			driver:  "oracle",
			user:    "admin",
			dsn:     "(description=...)",
			wantErr: false,
		},
		{
			name:    "unsafe db name: dash_test",
			driver:  "mysql",
			user:    "dash_user",
			dsn:     "dash_user:secret@tcp(127.0.0.1:3306)/dash_test?parseTime=true",
			wantErr: true,
			errMsg:  "dash_test",
		},
		{
			name:    "unsafe db name: test",
			driver:  "mysql",
			user:    "dash_user",
			dsn:     "dash_user:secret@tcp(127.0.0.1:3306)/test?parseTime=true",
			wantErr: true,
			errMsg:  "test",
		},
		{
			name:    "unsafe db name suffix: app_test",
			driver:  "mysql",
			user:    "dash_user",
			dsn:     "dash_user:secret@tcp(127.0.0.1:3306)/app_test?parseTime=true",
			wantErr: true,
			errMsg:  "_test",
		},
		{
			name:    "unsafe port: 33306 in dsn",
			driver:  "mysql",
			user:    "dash_user",
			dsn:     "dash_user:secret@tcp(127.0.0.1:33306)/dash?parseTime=true",
			wantErr: true,
			errMsg:  "33306",
		},
		{
			name:    "unsafe user: root in param",
			driver:  "mysql",
			user:    "root",
			dsn:     "dash_user:secret@tcp(127.0.0.1:3306)/dash?parseTime=true",
			wantErr: true,
			errMsg:  "root",
		},
		{
			name:    "unsafe user: root in dsn",
			driver:  "mysql",
			user:    "admin",
			dsn:     "root:secret@tcp(127.0.0.1:3306)/dash?parseTime=true",
			wantErr: true,
			errMsg:  "root",
		},
		{
			name:    "unsafe oracle root user",
			driver:  "oracle",
			user:    "root",
			dsn:     "oracle://...",
			wantErr: true,
			errMsg:  "root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckSafeDB(tt.driver, tt.user, tt.dsn)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckSafeDB() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errMsg != "" {
				if err == nil || !contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error message to contain %q, got %v", tt.errMsg, err)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || (len(s) > 0 && len(substr) > 0 && searchString(s, substr)))
}

func searchString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
