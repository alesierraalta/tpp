export class RealtimeClient {
  constructor(bus) {
    this.bus = bus;
    this.connected = false;
    this.last = null;
    this.subscribers = new Map();
  }

  // Open a session: record bus messages while it lasts.
  connect() {
    if (this.connected) return;
    this._session = (msg) => {
      this.last = msg;
    };
    this.bus.on("message", this._session);
    this.connected = true;
  }

  disconnect() {
    if (!this.connected) return;
    this.bus.off("message", this._session);
    this.connected = false;
  }

  // Bring up a fresh session on the same bus.
  reconnect() {
    this.connected = false;
    this.connect();
  }

  subscribe(handler) {
    const forward = (msg) => handler(msg);
    this.subscribers.set(handler, forward);
    this.bus.on("message", forward);
  }

  unsubscribe(handler) {
    const forward = this.subscribers.get(handler);
    this.subscribers.delete(handler);
    if (forward) this.bus.off("message", forward);
  }
}
