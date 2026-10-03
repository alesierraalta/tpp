export function createLedger() {
  const byId = new Map();
  return {
    // Same id posted twice is a no-op; the first amount wins.
    post({ id, amount }) {
      if (typeof id !== "string" || id.length === 0) throw new TypeError("id required");
      if (typeof amount !== "number" || !Number.isFinite(amount)) throw new TypeError("finite amount required");
      if (byId.has(id)) return { posted: false, id };
      byId.set(id, amount);
      return { posted: true, id };
    },
    balance() {
      let total = 0;
      for (const amount of byId.values()) total += amount;
      return total;
    },
    entries() {
      return [...byId.entries()].map(([id, amount]) => ({ id, amount }));
    },
  };
}
