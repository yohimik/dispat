// The PWA plugin's reload prompt, re-exported from the site's own theme folder.
//
// The plugin registers its theme directory so that `@theme/PwaReloadPopup`
// resolves from inside its bundled registerSw code, and under pnpm's strict
// node_modules layout that registration is fragile: a build can fail with
// "Can't resolve '@theme/PwaReloadPopup'" pointing into the plugin's lib. The
// site's `src/theme` folder sits first in the alias resolution on every build,
// so re-exporting the component here makes the alias hold regardless of how
// the plugin's own theme path fared. This is also the file to replace with a
// real component if the prompt ever needs customising.
export {default} from '@docusaurus/plugin-pwa/lib/theme/PwaReloadPopup';
