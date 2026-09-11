import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';
import downloadCounter from './index.ts';

test('local snapshots are served under the base URL only in client development', () => {
  const plugin = downloadCounter({siteDir: '/site', baseUrl: '/preview/'});
  const development = plugin.configureWebpack({mode: 'development'}, false);
  assert.deepEqual(development.devServer.static, [{
    directory: path.join('/site', 'data', 'download-counter'),
    publicPath: '/preview/',
    watch: false,
  }]);
  assert.deepEqual(plugin.configureWebpack({mode: 'production'}, false), {});
  assert.deepEqual(plugin.configureWebpack({mode: 'development'}, true), {});
  assert.equal(plugin.getPathsToWatch, undefined);
  assert.equal(plugin.contentLoaded, undefined);
});
