/**
 * 列表端点统一返回分页信封 {items:[...], total, page, page_size}（docs/12-api-spec.md §3 分页）。
 * 这里对「裸数组」与「信封」都兼容，并保证任何情况下都返回数组：
 * 上游一旦返回 null 或信封对象，页面上的 .map 会让整页崩掉（P1-27 F1）。
 *
 * 约束（P1-27 §3）：全仓库 grep \.items 在 web/src/ 下只允许出现在本函数内部。
 */
export function toItems<T>(res: unknown): T[] {
  if (Array.isArray(res)) {
    return res as T[];
  }
  const items = (res as { items?: unknown } | null | undefined)?.items;
  return Array.isArray(items) ? (items as T[]) : [];
}
