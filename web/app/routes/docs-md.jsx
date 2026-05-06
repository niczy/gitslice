import { docsMarkdown } from '../../src/docs.js';

export function loader() {
  return new Response(docsMarkdown, {
    headers: {
      'Cache-Control': 'no-cache',
      'Content-Type': 'text/markdown; charset=utf-8',
    },
  });
}
