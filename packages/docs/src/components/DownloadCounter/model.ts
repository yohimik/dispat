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
  let pulls = 0;
  for (const item of data.repositories) {
    if (!item || !repositories.has(item.repository) || names.has(item.repository) || !safe(item.pulls)) throw new Error('invalid repository total');
    names.add(item.repository);
    pulls += item.pulls;
  }
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

type PollerOptions = {
  load(signal: AbortSignal): Promise<unknown>;
  value(snapshot: DownloadSnapshot): void;
  failure(error: unknown): void;
  visible(): boolean;
  listen(listener: () => void): () => void;
  timer(callback: () => void, delay: number): unknown;
  clearTimer(id: unknown): void;
  interval?: number;
};

export function startPolling(options: PollerOptions): () => void {
  let stopped = false;
  let timer: unknown;
  let request: AbortController | undefined;
  let revision = 0;
  const clear = () => { if (timer !== undefined) options.clearTimer(timer); timer = undefined; };
  const schedule = () => {
    clear();
    if (!stopped && options.visible()) timer = options.timer(run, options.interval ?? REFRESH_MS);
  };
  const run = () => {
    if (stopped || !options.visible() || request) return;
    const current = ++revision;
    const active = new AbortController();
    request = active;
    options.load(active.signal).then(validateSnapshot).then((snapshot) => {
      if (!stopped && current === revision) options.value(snapshot);
    }).catch((error) => {
      if (!stopped && current === revision && !(error instanceof DOMException && error.name === 'AbortError')) options.failure(error);
    }).finally(() => { if (request === active) { request = undefined; schedule(); } });
  };
  const unlisten = options.listen(() => {
    clear();
    if (!options.visible()) { revision++; request?.abort(); request = undefined; }
    else run();
  });
  run();
  return () => { stopped = true; revision++; clear(); request?.abort(); request = undefined; unlisten(); };
}
