import path from 'node:path';
import type {LoadContext, Plugin} from '@docusaurus/types';

// Docusaurus merges this supported extension into webpack-dev-server's
// configuration, but its public webpack type does not declare devServer.
type WebpackConfig = Parameters<NonNullable<Plugin['configureWebpack']>>[0];
type DevelopmentConfig = WebpackConfig & {
  devServer?: {static: Array<{directory: string; publicPath: string; watch: boolean}>};
};

// Only the development server sees the generated snapshot. Putting it in
// staticDirectories would also freeze it into production builds and the PWA.
export default function downloadCounter(context: LoadContext): Plugin {
  return {
    name: 'local-download-counter',
    configureWebpack(config, isServer): DevelopmentConfig {
      if (isServer || config.mode !== 'development') return {};
      return {
        devServer: {
          static: [{
            directory: path.join(context.siteDir, 'data', 'download-counter'),
            publicPath: context.baseUrl,
            watch: false,
          }],
        },
      };
    },
  };
}
