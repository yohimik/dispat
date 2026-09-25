export const REFRESH_MS = 15 * 60 * 1000;
export const STALE_MS = 30 * 60 * 1000;

export type DownloadSnapshot = {
  schemaVersion: 1;
  total: number;
  github: number;
  dockerHub: number;
  collectedAt: string;
  repositories: Array<{repository: string; pulls: number}>;
};

const repositories = new Set([
  'yohimik/dispat-alpine', 'yohimik/dispat-debian', 'yohimik/dispat-ubuntu', 'yohimik/dispat-dind',
]);

function safe(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 0;
}

export function validateSnapshot(value: unknown): DownloadSnapshot {
  if (!value || typeof value !== 'object') throw new Error('snapshot must be an object');
  const data = value as Partial<DownloadSnapshot>;
  if (data.schemaVersion !== 1 || !safe(data.total) || !safe(data.github) || !safe(data.dockerHub)) {
    throw new Error('invalid snapshot totals');
  }
  const time = typeof data.collectedAt === 'string' ? Date.parse(data.collectedAt) : Number.NaN;
  const timestamp = typeof data.collectedAt === 'string'
    ? /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,3}))?Z$/.exec(data.collectedAt)
    : null;
  const normalized = timestamp ? `${timestamp[1]}.${(timestamp[2] ?? '').padEnd(3, '0')}Z` : '';
  if (!Number.isFinite(time) || !timestamp || new Date(time).toISOString() !== normalized) throw new Error('invalid collection time');
  if (!Array.isArray(data.repositories) || data.repositories.length !== repositories.size) throw new Error('invalid repositories');
  const names = new Set<string>();
  for (const item of data.repositories) {
    if (!item || !repositories.has(item.repository) || names.has(item.repository) || !safe(item.pulls)) throw new Error('invalid repository total');
    names.add(item.repository);
  }
  const pulls = data.repositories.reduce((total, item) => total + item.pulls, 0);
  if (!Number.isSafeInteger(pulls) || pulls !== data.dockerHub || data.github + data.dockerHub !== data.total) {
    throw new Error('inconsistent snapshot totals');
  }
  return data as DownloadSnapshot;
}

export function displayDigits(total: number | undefined, minimum = 8): string[] {
  return String(total ?? 0).padStart(minimum, '0').split('');
}

export function formatCounter(total: number | undefined): string {
  return displayDigits(total).join('').replace(/\B(?=(\d{3})+$)/g, '.');
}

interface PollerOptions {
  load(signal: AbortSignal): Promise<unknown>;
  value(snapshot: DownloadSnapshot): void;
  failure(error: unknown): void;
  visible(): boolean;
  listen(listener: () => void): () => void;
  timer(callback: () => void, delay: number): unknown;
  clearTimer(id: unknown): void;
  interval?: number;
}

/**
 * One polling loop and the state it owns: whether it stopped, the pending
 * timer, the request in flight and the revision a late answer is checked
 * against. A revision only ever moves forward, so an answer to a request the
 * page has since hidden or stopped is dropped.
 */
class DownloadPoller {
  private readonly options: PollerOptions;
  private readonly unlisten: () => void;
  private isStopped = false;
  private timer: unknown;
  private request: AbortController | undefined;
  private revision = 0;

  constructor(options: PollerOptions) {
    this.options = options;
    this.unlisten = options.listen(() => this.changeVisibility());
  }

  run(): void {
    if (this.isStopped || !this.options.visible() || this.request) return;
    const current = ++this.revision;
    const active = new AbortController();
    this.request = active;
    this.options.load(active.signal).then(validateSnapshot).then((snapshot) => {
      if (!this.isStopped && current === this.revision) this.options.value(snapshot);
    }).catch((error) => {
      if (!this.isStopped && current === this.revision && !(error instanceof DOMException && error.name === 'AbortError')) this.options.failure(error);
    }).finally(() => { if (this.request === active) { this.request = undefined; this.schedule(); } });
  }

  stop(): void {
    this.isStopped = true;
    this.revision++;
    this.clear();
    this.request?.abort();
    this.request = undefined;
    this.unlisten();
  }

  private clear(): void {
    if (this.timer !== undefined) this.options.clearTimer(this.timer);
    this.timer = undefined;
  }

  private schedule(): void {
    this.clear();
    if (!this.isStopped && this.options.visible()) this.timer = this.options.timer(() => this.run(), this.options.interval ?? REFRESH_MS);
  }

  private changeVisibility(): void {
    this.clear();
    if (!this.options.visible()) {
      this.revision++;
      this.request?.abort();
      this.request = undefined;
      return;
    }
    this.run();
  }
}

export function startPolling(options: PollerOptions): () => void {
  const poller = new DownloadPoller(options);
  poller.run();
  return () => poller.stop();
}
