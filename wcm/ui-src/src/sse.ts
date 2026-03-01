type Handler = (data: unknown) => void;

const SSE_EVENTS = ["approval_requested", "approval_resolved", "lock_state_changed"] as const;

export class SSEClient {
  private es: EventSource | null = null;
  private handlers = new Map<string, Handler[]>();
  private lastEventId = "";
  private retryMs = 1_000;
  private stopped = false;

  constructor(
    private readonly url: string,
    private readonly onConnectionChange: (connected: boolean) => void,
  ) {
    this.connect();
  }

  on(type: string, handler: Handler): this {
    const list = this.handlers.get(type) ?? [];
    list.push(handler);
    this.handlers.set(type, list);
    return this;
  }

  close(): void {
    this.stopped = true;
    this.es?.close();
  }

  private connect(): void {
    if (this.stopped) return;

    const url = this.lastEventId
      ? `${this.url}?lastEventId=${encodeURIComponent(this.lastEventId)}`
      : this.url;

    this.es = new EventSource(url);

    this.es.onopen = () => {
      this.retryMs = 1_000;
      this.onConnectionChange(true);
    };

    this.es.onerror = () => {
      this.onConnectionChange(false);
      this.es?.close();
      setTimeout(() => this.connect(), this.retryMs);
      this.retryMs = Math.min(this.retryMs * 2, 30_000);
    };

    for (const type of SSE_EVENTS) {
      this.es.addEventListener(type, (e: Event) => {
        const msg = e as MessageEvent;
        if (msg.lastEventId) this.lastEventId = msg.lastEventId;
        try {
          const data = JSON.parse(msg.data as string) as unknown;
          this.handlers.get(type)?.forEach(h => h(data));
        } catch {
          // ignore malformed events
        }
      });
    }
  }
}
