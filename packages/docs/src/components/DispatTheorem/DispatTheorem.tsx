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

const MODEL_URL = 'https://github.com/yohimik/dispat/blob/main/specs/ccme-spec/SPEC.md#1311-complexity-and-scale';
const FORMULA_TEX = String.raw`T=\min_{\pi\in\Pi}\iint_{[0,\infty)^2}(D+E)f_\pi(D,E)\,\mathrm{d}D\,\mathrm{d}E`;
const DIFFERENTIAL_SPACING = '0.1667em';
const EMPTY_CLASS_NAME = '';

export const DispatTheorem: FC<DispatTheoremProps> = (props) => {
  const {className = EMPTY_CLASS_NAME} = props;

  return (
    <figure className={`${styles.theorem} ${className}`} aria-labelledby="dispat-theorem-title">
      <div className={styles.header}>
        <span className={styles.sectionNumber}>THE DISPAT OBJECTIVE</span>
        <span>§ 13.11</span>
      </div>
      <div className={styles.statement}>
        <span className={styles.label} id="dispat-theorem-title">Minimum expected modeled finish time</span>
        <math className={styles.formula} display="block" aria-label="T equals the minimum over strategies pi in capital Pi of the double integral over nonnegative D and E of the sum of D and E multiplied by their joint probability density f sub pi, with respect to D and E">
          <semantics>
            <mrow>
              <mi>T</mi>
              <mo>=</mo>
              <munder>
                <mo>min</mo>
                <mrow><mi>π</mi><mo>∈</mo><mi>Π</mi></mrow>
              </munder>
              <munder>
                <mo>∬</mo>
                <msup>
                  <mrow><mo>[</mo><mn>0</mn><mo>,</mo><mo>∞</mo><mo>)</mo></mrow>
                  <mn>2</mn>
                </msup>
              </munder>
              <mo>(</mo>
              <mi>D</mi>
              <mo>+</mo>
              <mi>E</mi>
              <mo>)</mo>
              <msub><mi>f</mi><mi>π</mi></msub>
              <mo>(</mo><mi>D</mi><mo>,</mo><mi>E</mi><mo>)</mo>
              <mspace width={DIFFERENTIAL_SPACING} />
              <mi mathvariant="normal">d</mi>
              <mi>D</mi>
              <mspace width={DIFFERENTIAL_SPACING} />
              <mi mathvariant="normal">d</mi>
              <mi>E</mi>
            </mrow>
            <annotation encoding="application/x-tex">{FORMULA_TEX}</annotation>
          </semantics>
        </math>
        <p className={styles.translation}>Choose the feasible execution strategy with the lowest expected completion time.</p>
        <p className={styles.notation}><i>Π</i> is a finite, nonempty set of strategies for the same workload model and resource constraints. <i>D</i> and <i>E</i> are discovery and remaining time on the same critical path; <i>f</i><sub>π</sub> is their normalized joint probability density.</p>
        <p className={styles.notation}>All work and delays are included. Strategies are chosen before uncertain outcomes, and all expected durations are finite. The density gives <i>T</i> units of time. Timing laws without a joint density use the corresponding probability-measure integral. This defines an objective; it does not claim that dispat finds the minimizing strategy.</p>
      </div>
      <Link className={styles.proof} to={MODEL_URL}>Read the scheduling model <span aria-hidden="true">↗</span></Link>
    </figure>
  );
};
