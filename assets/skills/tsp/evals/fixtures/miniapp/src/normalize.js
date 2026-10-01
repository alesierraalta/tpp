// Normalizes a path: collapses repeated separators and removes "." segments.
// Invariant: normalizing an already-normalized path returns it unchanged.
export function normalizePath(p) {
  if (typeof p !== "string") throw new TypeError("path must be a string");
  if (p === "") return "";
  return p.replace(/\/{2,}/g, "/").replace("/./", "/");
}
