// The plugin's name, on its own so the landing page can ask for its data
// without importing the plugin: the plugin runs in Node and pulls in fs, path
// and the Docusaurus logger, none of which belong in the browser bundle.
export const README_PLUGIN = 'dispat-readme';
