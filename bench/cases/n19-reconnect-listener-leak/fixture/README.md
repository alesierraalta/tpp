# realtime-client

Keeps a live session on an injected event bus and manages message subscribers.

- `new RealtimeClient(bus)` binds the client to any emitter that offers `on`, `off` and
  `listenerCount` (an in-memory `EventEmitter` is enough).
- `connect()` opens a session: while connected, every bus `message` is recorded as
  `client.last`. `disconnect()` closes the session; a client that has been disconnected
  records nothing more.
- `reconnect()` re-establishes the session on the same bus, replacing the one before it:
  however many times a client has been reconnected, it holds exactly one session listener
  on the bus while connected, and none once it is disconnected.
- `subscribe(handler)` registers a handler that receives every bus `message`;
  `unsubscribe(handler)` removes it again, and after `unsubscribe` the handler is never
  called and holds no listener on the bus.
