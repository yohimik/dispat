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
const FORMULA_TEX = String.raw`T=\min\left(\iint(D+E)\,\mathrm{d}D\,\mathrm{d}E\right)`;
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
        <span className={styles.label} id="dispat-theorem-title">A symbolic optimization target</span>
        <math className={styles.formula} display="block" aria-label="T equals the minimum of the double integral of D plus E with respect to D and E">
          <semantics>
            <mrow>
              <mi>T</mi>
              <mo>=</mo>
              <mo>min</mo>
              <mo>(</mo>
              <mo>∬</mo>
              <mo>(</mo>
              <mi>D</mi>
              <mo>+</mo>
              <mi>E</mi>
              <mo>)</mo>
              <mspace width={DIFFERENTIAL_SPACING} />
              <mi mathvariant="normal">d</mi>
              <mi>D</mi>
              <mspace width={DIFFERENTIAL_SPACING} />
              <mi mathvariant="normal">d</mi>
              <mi>E</mi>
              <mo>)</mo>
            </mrow>
            <annotation encoding="application/x-tex">{FORMULA_TEX}</annotation>
          </semantics>
        </math>
        <p className={styles.translation}>Optimize discovery and execution together.</p>
        <p className={styles.notation}><i>D</i> represents discovery and input acquisition. <i>E</i> represents execution and other modeled overhead. <i>T</i> is a symbolic objective; its integration domain, weighting, and admissible plans remain to be specified.</p>
      </div>
      <Link className={styles.proof} to={MODEL_URL}>Read the scheduling model <span aria-hidden="true">↗</span></Link>
    </figure>
  );
};
