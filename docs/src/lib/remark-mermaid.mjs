import { visit } from 'unist-util-visit';

// Turn ```mermaid fenced code blocks into a raw <pre class="mermaid"> element so
// they bypass Shiki highlighting and can be rendered client-side by mermaid.js
// (see the injected client script in astro.config.mjs). The source is
// HTML-escaped; mermaid reads it back from textContent.
export default function remarkMermaid() {
  return (tree) => {
    visit(tree, 'code', (node, index, parent) => {
      if (!parent || node.lang !== 'mermaid') return;
      const escaped = node.value
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;');
      parent.children.splice(index, 1, {
        type: 'html',
        value: `<pre class="mermaid" data-mermaid>${escaped}</pre>`,
      });
    });
  };
}
