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
const FORMULA_TEX = String.raw`T_{\min}=L=\max_{p\in P(G)}\sum_{i\in p}d_i`;
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
        <math className={styles.formula} display="block" aria-label="T minimum equals L equals the maximum over paths p in P of G of the sum over tasks i in p of d i">
          <semantics>
            <mrow>
              <msub><mi>T</mi><mtext>min</mtext></msub>
              <mo>=</mo>
              <mi>L</mi>
              <mo>=</mo>
              <munder>
                <mo>max</mo>
                <mrow><mi>p</mi><mo>∈</mo><mi>P</mi><mo>(</mo><mi>G</mi><mo>)</mo></mrow>
              </munder>
              <munder>
                <mo>∑</mo>
                <mrow><mi>i</mi><mo>∈</mo><mi>p</mi></mrow>
              </munder>
              <msub><mi>d</mi><mi>i</mi></msub>
            </mrow>
            <annotation encoding="application/x-tex">{FORMULA_TEX}</annotation>
          </semantics>
        </math>
        <p className={styles.translation}>The longest dependency path sets the fastest possible finish.</p>
        <p className={styles.notation}><i>G</i> is the task graph, <i>d</i><sub>i</sub> a task’s duration, and <i>L</i> the critical path’s duration.</p>
      </div>
      <figcaption className={styles.conditions}>
        <span className={styles.conditionsTitle}>Exact under these ideal conditions</span>
        <ul>
          <li>One fixed dependency graph and task durations.</li>
          <li>No failures, transfer time, or scheduling overhead.</li>
          <li>Enough compatible capacity to start every ready task immediately.</li>
        </ul>
      </figcaption>
      <Link className={styles.proof} to={PROOF_URL}>Read the proof in the specification <span aria-hidden="true">↗</span></Link>
    </figure>
  );
};
