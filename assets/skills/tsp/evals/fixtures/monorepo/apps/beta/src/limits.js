// A value is within the limit when it does not exceed max: n > max is out, n === max is in.
export function withinLimit(n, max) {
  if (typeof n !== "number" || typeof max !== "number") throw new TypeError("numbers required");
  return !(n > max);
}

export function clamp(n, lo, hi) {
  if (lo > hi) throw new RangeError("lo must not exceed hi");
  if (n < lo) return lo;
  if (n > hi) return hi;
  return n;
}
