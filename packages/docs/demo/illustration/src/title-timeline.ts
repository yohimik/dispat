export type TitleCue = {
  text: string;
  start: number;
  end: number;
};

export const MASTER_DURATION = 900;

export const MASTER_TITLES: readonly TitleCue[] = [
  {text: 'A package is a folder. The graph comes from its manifests.', start: 15, end: 140},
  {text: 'The commit decides the blast radius.', start: 150, end: 330},
  {text: 'Builds and publishes in dependency order, in parallel.', start: 340, end: 560},
  {text: 'An error in the middle stays contained.', start: 565, end: 670},
  {text: 'Fix it, commit it, then run the same command.', start: 670, end: 885},
];

export const isTitleVisible = (cue: TitleCue, frame: number): boolean =>
  frame > cue.start && frame < cue.end;
