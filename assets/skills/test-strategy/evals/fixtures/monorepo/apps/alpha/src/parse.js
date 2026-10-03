// Splits one CSV line into fields; double quotes delimit a field and are removed.
export function parseCsvLine(line) {
  if (typeof line !== "string") throw new TypeError("line must be a string");
  if (line.length === 0) return [];
  return line.split(",").map((field) => {
    const trimmed = field.trim();
    if (trimmed.startsWith('"') && trimmed.endsWith('"') && trimmed.length >= 2) {
      return trimmed.slice(1, -1);
    }
    return trimmed;
  });
}
