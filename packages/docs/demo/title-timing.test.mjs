import assert from 'node:assert/strict';
import test from 'node:test';

import {
  isTitleVisible,
  MASTER_DURATION,
  MASTER_TITLES,
} from './illustration/src/title-timeline.ts';

test('Master shows at most one title at every frame', () => {
  for (let frame = 0; frame < MASTER_DURATION; frame += 1) {
    const visible = MASTER_TITLES.filter((cue) => isTitleVisible(cue, frame));
    assert.ok(
      visible.length <= 1,
      `frame ${frame} shows overlapping titles: ${visible.map(({text}) => JSON.stringify(text)).join(', ')}`,
    );
  }
});

test('every Master title has a nonempty display interval', () => {
  for (const cue of MASTER_TITLES) {
    assert.ok(cue.text.length > 0, 'title text must not be empty');
    assert.ok(cue.start >= 0, `${JSON.stringify(cue.text)} starts before the composition`);
    assert.ok(cue.end <= MASTER_DURATION, `${JSON.stringify(cue.text)} ends after the composition`);
    assert.ok(cue.end - cue.start > 1, `${JSON.stringify(cue.text)} has no visible frame`);
  }
});
