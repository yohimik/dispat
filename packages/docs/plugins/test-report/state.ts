import type {ArchivedReport, HistoricalEvidence, Report} from './types';

export type ArchivedState = {
  report: Report | null;
  evidence: HistoricalEvidence | null;
  status: 'available' | 'loading' | 'unavailable';
};

/** Resolve only an archive loaded for the active version; late responses never cross versions. */
export function resolveArchivedState(
  version: string,
  isArchived: boolean,
  loaded: {version: string; value: ArchivedReport | null} | null,
): ArchivedState {
  if (!isArchived) return {report: null, evidence: null, status: 'unavailable'};
  if (loaded?.version !== version) return {report: null, evidence: null, status: 'loading'};
  return {
    report: loaded.value?.report ?? null,
    evidence: loaded.value?.evidence ?? null,
    status: loaded.value ? 'available' : 'unavailable',
  };
}
