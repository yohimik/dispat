import Link from '@docusaurus/Link';
import type {Inline} from '@site/plugins/readme/types';
import React from 'react';

// Renders the inline-markdown tokens the readme plugin parsed out of the
// repository README and the CLI one.
//
// The plugin cannot hand over React elements — global data is JSON — so the
// markdown crosses as a token tree and becomes elements here. Links are the
// reason it is a tree at all: an internal one has to go through <Link to> so
// Docusaurus routes it client-side and the build's link checker sees it.

function Token({token}: {token: Inline}): React.ReactElement {
  switch (token.t) {
    case 'text':
      return <>{token.v}</>;
    case 'code':
      return <code>{token.v}</code>;
    case 'strong':
      return (
        <strong>
          <Inlines tokens={token.v} />
        </strong>
      );
    case 'em':
      return (
        <em>
          <Inlines tokens={token.v} />
        </em>
      );
    case 'link':
      // `to` for a route on this site, `href` for anywhere else: Docusaurus
      // prefixes `to` with baseUrl and would send /https://… otherwise.
      return token.internal ? (
        <Link to={token.href}>
          <Inlines tokens={token.v} />
        </Link>
      ) : (
        <Link href={token.href}>
          <Inlines tokens={token.v} />
        </Link>
      );
  }
}

export default function Inlines({tokens}: {tokens: Inline[]}): React.ReactElement {
  return (
    <>
      {tokens.map((token, i) => (
        // The list is static — it comes from a file read at build time and
        // never reorders — so the index is a stable key.
        // eslint-disable-next-line react/no-array-index-key
        <Token key={i} token={token} />
      ))}
    </>
  );
}
