import type {HTMLAttributes} from 'react';

type MathMLAttributes = HTMLAttributes<HTMLElement> & {
  display?: 'block' | 'inline';
  encoding?: string;
  mathvariant?: 'normal';
  width?: string;
};

declare module 'react' {
  namespace JSX {
    interface IntrinsicElements {
      math: MathMLAttributes;
      semantics: MathMLAttributes;
      mrow: MathMLAttributes;
      mspace: MathMLAttributes;
      msub: MathMLAttributes;
      munder: MathMLAttributes;
      mtext: MathMLAttributes;
      mi: MathMLAttributes;
      mo: MathMLAttributes;
      annotation: MathMLAttributes;
    }
  }
}
