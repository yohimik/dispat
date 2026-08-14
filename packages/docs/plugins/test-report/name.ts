// The plugin's name, on its own so a page can ask for its data without
// importing the plugin: the plugin runs in Node and pulls in fs, path and the
// Docusaurus logger, none of which belong in the browser bundle.
export const TEST_REPORT_PLUGIN = 'dispat-test-report';
