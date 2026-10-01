// Renders fields back into one CSV line; a field containing the separator is quoted.
export function renderCsvLine(fields) {
  if (!Array.isArray(fields)) throw new TypeError("fields must be an array");
  return fields
    .map((field) => {
      const value = String(field);
      return value.includes(",") ? `"${value}"` : value;
    })
    .join(",");
}
