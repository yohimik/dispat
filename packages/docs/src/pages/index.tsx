import {Redirect} from '@docusaurus/router';
import React from 'react';

// The docs own the root (docs.routeBasePath === '/'), but no page claims '/'
// itself. Send it to the entry point rather than serving a 404.
export default function Home(): React.ReactElement {
  return <Redirect to="/getting-started" />;
}
