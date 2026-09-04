import React from 'react';
import {Composition} from 'remotion';
import {Master, Scene, SCENES} from './Master';
import {Blast, BLAST_DURATION} from './Blast';
import {Compute, COMPUTE_DURATION} from './Compute';
import {Control, CONTROL_DURATION} from './Control';
import {Glue, GLUE_DURATION} from './Glue';
import {Hooks, HOOKS_DURATION} from './Hooks';
import {Lock, LOCK_DURATION} from './Lock';
import {Math_, MATH_DURATION} from './Math';
import {Order, ORDER_DURATION} from './Order';
import {Polyglot, POLYGLOT_DURATION} from './Polyglot';
import {Polyrepo, POLYREPO_DURATION} from './Polyrepo';
import {Run, RUN_DURATION} from './Run';
import {Single, SINGLE_DURATION} from './Single';
import {Terminal, TERMINAL_DURATION} from './Terminal';
import {Why, WHY_DURATION} from './Why';
import {Aqua, AQUA_DURATION} from './Aqua';

// Twenty frames per second across the board: the storyboards are written in
// frames, so one number here is what sets the deck's unhurried pace.
const FPS = 20;
const SIZE = {fps: FPS, width: 1920, height: 1080} as const;

export const Root: React.FC = () => (
  <>
    <Composition id="Master" component={Master} durationInFrames={900} {...SIZE} />
    {/* Blast twice: titled for the commit-messages page's gif, and as the
        landing page's clip, which leaves the top strip to the page's own
        feature-text overlay. */}
    <Composition id="Blast" component={Blast} durationInFrames={BLAST_DURATION} {...SIZE} />
    <Composition id="BlastClip" component={Blast} defaultProps={{clip: true}} durationInFrames={BLAST_DURATION} {...SIZE} />
    <Composition id="Order" component={Order} durationInFrames={ORDER_DURATION} {...SIZE} />
    <Composition id="Control" component={Control} durationInFrames={CONTROL_DURATION} {...SIZE} />
    <Composition id="Polyglot" component={Polyglot} durationInFrames={POLYGLOT_DURATION} {...SIZE} />
    <Composition id="Terminal" component={Terminal} durationInFrames={TERMINAL_DURATION} {...SIZE} />
    <Composition id="Compute" component={Compute} durationInFrames={COMPUTE_DURATION} {...SIZE} />
    <Composition id="Run" component={Run} durationInFrames={RUN_DURATION} {...SIZE} />
    <Composition id="Single" component={Single} durationInFrames={SINGLE_DURATION} {...SIZE} />
    <Composition id="Hooks" component={Hooks} durationInFrames={HOOKS_DURATION} {...SIZE} />
    <Composition id="Polyrepo" component={Polyrepo} durationInFrames={POLYREPO_DURATION} {...SIZE} />
    <Composition id="Lock" component={Lock} durationInFrames={LOCK_DURATION} {...SIZE} />
    <Composition id="Glue" component={Glue} durationInFrames={GLUE_DURATION} {...SIZE} />
    <Composition id="Math" component={Math_} durationInFrames={MATH_DURATION} {...SIZE} />
    <Composition id="Why" component={Why} durationInFrames={WHY_DURATION} {...SIZE} />
    <Composition id="Aqua" component={Aqua} durationInFrames={AQUA_DURATION} {...SIZE} />
    {SCENES.map((s) => (
      <Composition
        key={s.id}
        id={s.id}
        component={Scene}
        defaultProps={{from: s.from}}
        durationInFrames={s.to - s.from}
        {...SIZE}
      />
    ))}
  </>
);
