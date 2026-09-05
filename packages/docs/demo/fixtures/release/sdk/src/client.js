export const releaseClient = () => fetch('/version').then((response) => response.text());
