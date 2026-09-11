import useBaseUrl from '@docusaurus/useBaseUrl';
import React from 'react';
import {displayDigits, formatCounter, startPolling, STALE_MS, type DownloadSnapshot} from './model';
import styles from './styles.module.css';

export default function DownloadCounter(): React.ReactElement {
  const url = useBaseUrl('/downloads.json');
  const [snapshot, setSnapshot] = React.useState<DownloadSnapshot>();
  const [failed, setFailed] = React.useState(false);
  React.useEffect(() => startPolling({
    load: async (signal) => {
      const timeout = AbortSignal.timeout(20_000);
      const response = await fetch(url, {cache: 'no-store', signal: AbortSignal.any([signal, timeout])});
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      return response.json();
    },
    value: (next) => { setSnapshot(next); setFailed(false); },
    failure: () => setFailed(true),
    visible: () => document.visibilityState === 'visible',
    listen: (listener) => { document.addEventListener('visibilitychange', listener); return () => document.removeEventListener('visibilitychange', listener); },
    timer: (callback, delay) => window.setTimeout(callback, delay),
    clearTimer: (id) => window.clearTimeout(id as number),
  }), [url]);
  const loading = !snapshot && !failed;
  const digits = displayDigits(snapshot?.total);
  const stale = snapshot ? Date.now() - Date.parse(snapshot.collectedAt) > STALE_MS : false;
  const status = loading ? 'Loading distribution count' : !snapshot ? 'Distribution count unavailable' :
    `${snapshot.total.toLocaleString()} distribution events, collected ${new Date(snapshot.collectedAt).toLocaleString()}${(failed || stale) ? ', refresh delayed' : ''}`;
  return (
    <div className={styles.counter}>
      <span className={styles.screenReaderOnly} role="status" aria-live="polite" aria-atomic="true">{status}</span>
      <div className={styles.digits} aria-hidden="true" style={{'--digits': digits.length} as React.CSSProperties}>
        {formatCounter(snapshot?.total).split('').map((digit, index) => digit === '.' ? (
          <span className={styles.separator} key={index}>.</span>
        ) : (
          <span className={styles.digit} key={index}>
            <span className={`${styles.reel} ${loading ? styles.loading : styles.settled}`} style={{'--digit': digit} as React.CSSProperties} key={`${index}-${digit}`}>
              {'01234567890123456789'.split('').map((number, reelIndex) => <span key={reelIndex}>{number}</span>)}
            </span>
          </span>
        ))}
      </div>
      <p className={styles.label}>GitHub release asset downloads + Docker Hub pulls</p>
      <p className={styles.freshness}>
        {snapshot ? <>Collected {new Date(snapshot.collectedAt).toLocaleString()}{(failed || stale) && ' · refresh delayed'}</> : loading ? 'Loading count…' : 'Count unavailable'}
      </p>
    </div>
  );
}
