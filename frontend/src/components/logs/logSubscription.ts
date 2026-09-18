interface LogTransport {
  getHandlers(): Map<string, Set<(message: any) => void>>;
  sendWS(message: any): void;
}

export function subscribeLogStream(
  transport: LogTransport,
  events: EventTarget,
  payload: Record<string, unknown> & { key: string },
  handler: (message: any) => void,
  onRestart: () => void,
  onDisconnect: () => void,
) {
  const handlers = transport.getHandlers();
  if (!handlers.has('logs')) handlers.set('logs', new Set());
  handlers.get('logs')!.add(handler);

  const start = () => {
    onRestart();
    transport.sendWS({ type: 'logs', payload: { ...payload, action: 'start' } });
  };
  const connectionState = (event: Event) => {
    const detail = (event as CustomEvent).detail;
    if (detail?.type === 'websocket' && detail.state !== 'connected') onDisconnect();
  };
  const restored = (event: Event) => {
    // A healthy window becoming visible also emits this event; keep its history.
    if ((event as CustomEvent).detail?.reason !== 'visibility') start();
  };
  events.addEventListener('connection:restored', restored);
  events.addEventListener('connection:state', connectionState);
  start();

  return () => {
    events.removeEventListener('connection:restored', restored);
    events.removeEventListener('connection:state', connectionState);
    transport.sendWS({ type: 'logs', payload: { action: 'stop', key: payload.key } });
    const current = handlers.get('logs');
    current?.delete(handler);
    if (current?.size === 0) handlers.delete('logs');
  };
}
