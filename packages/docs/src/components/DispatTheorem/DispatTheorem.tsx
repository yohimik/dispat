// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

import Link from '@docusaurus/Link';
import type {FC} from 'react';

import styles from './styles.module.css';

export interface BaseDispatTheoremProps {
  className?: string;
}

export interface DispatTheoremProps extends BaseDispatTheoremProps {
  children?: never;
}

const PROOF_URL = 'https://github.com/yohimik/dispat/blob/main/specs/ccme-spec/SPEC.md#1311-complexity-and-scale';
const FORMULA_TEX = String.raw`t=\max(D+E)`;
const EMPTY_CLASS_NAME = '';

export const DispatTheorem: FC<DispatTheoremProps> = (props) => {
  const {className = EMPTY_CLASS_NAME} = props;

  return (
    <figure className={`${styles.theorem} ${className}`} aria-labelledby="dispat-theorem-title">
      <div className={styles.header}>
        <span className={styles.sectionNumber}>THE DISPAT THEOREM</span>
        <span>§ 13.11</span>
      </div>
      <div className={styles.statement}>
        <span className={styles.label} id="dispat-theorem-title">The ideal finish time</span>
        <math className={styles.formula} display="block" aria-label="t equals the maximum over complete dependency paths of discovery and acquisition time D plus execution and other modeled time E on the same path">
          <semantics>
            <mrow>
              <mi>t</mi>
              <mo>=</mo>
              <mo>max</mo>
              <mo>(</mo>
              <mi>D</mi>
              <mo>+</mo>
              <mi>E</mi>
              <mo>)</mo>
            </mrow>
            <annotation encoding="application/x-tex">{FORMULA_TEX}</annotation>
          </semantics>
        </math>
        <p className={styles.translation}>The slowest complete dependency path sets the ideal finish time.</p>
        <p className={styles.notation}>For each complete dependency path, <i>D</i> totals discovery and input acquisition. <i>E</i> totals the remaining execution, overhead, and waits. Take the largest combined total.</p>
      </div>
      <figcaption className={styles.conditions}>
        <span className={styles.conditionsTitle}>Exact under these ideal conditions</span>
        <ul>
          <li>One fixed, finite dependency graph with no cycles includes all required work and delays.</li>
          <li>Enough compatible capacity to start every ready step immediately.</li>
          <li>No failures or unmodeled waits.</li>
        </ul>
      </figcaption>
      <Link className={styles.proof} to={PROOF_URL}>Read the proof in the specification <span aria-hidden="true">↗</span></Link>
    </figure>
  );
};
