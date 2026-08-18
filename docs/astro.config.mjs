// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import remarkMermaid from './src/lib/remark-mermaid.mjs';

// Client-side mermaid renderer, injected on every page (bundled — no CDN, so it
// works under a strict CSP / offline). Renders <pre class="mermaid"> blocks and
// re-renders on Starlight's light/dark toggle and on client navigation.
const mermaidClient = `
import mermaid from 'mermaid';
function currentTheme() {
  return document.documentElement.dataset.theme === 'light' ? 'default' : 'dark';
}
async function renderMermaid() {
  const blocks = document.querySelectorAll('pre.mermaid[data-mermaid]');
  if (!blocks.length) return;
  blocks.forEach((el) => {
    if (el.dataset.src === undefined) el.dataset.src = el.textContent;
    el.removeAttribute('data-processed');
    el.innerHTML = el.dataset.src;
  });
  // 'loose' lets node labels use <br/> etc.; docs content is first-party/trusted.
  mermaid.initialize({ startOnLoad: false, theme: currentTheme(), securityLevel: 'loose' });
  await mermaid.run({ querySelector: 'pre.mermaid[data-mermaid]' });
}
document.addEventListener('astro:page-load', renderMermaid);
new MutationObserver(renderMermaid).observe(document.documentElement, {
  attributes: true,
  attributeFilter: ['data-theme'],
});
`;

// https://astro.build/config
export default defineConfig({
  site: 'https://sag-solutions.github.io',
  base: '/otel-fleet',
  markdown: {
    remarkPlugins: [remarkMermaid],
  },
  integrations: [
    {
      name: 'mermaid-client',
      hooks: {
        'astro:config:setup': ({ injectScript }) => {
          injectScript('page', mermaidClient);
        },
      },
    },
    starlight({
      title: 'otel-fleet',
      description:
        'Self-hosted, multi-tenant OpenTelemetry collector fleet management.',
      favicon: '/favicon.svg',
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/SAG-Solutions/otel-fleet',
        },
      ],
      editLink: {
        baseUrl: 'https://github.com/SAG-Solutions/otel-fleet/edit/main/docs/',
      },
      components: {
        SiteTitle: './src/components/SiteTitle.astro',
      },
      customCss: [
        '@fontsource/red-hat-display/300.css',
        '@fontsource/red-hat-display/400.css',
        '@fontsource/red-hat-display/500.css',
        '@fontsource/red-hat-display/600.css',
        '@fontsource/red-hat-display/700.css',
        '@fontsource/red-hat-display/900.css',
        './src/styles/sag.css',
      ],
      sidebar: [
        { label: 'Home', link: '/' },
        { label: 'Quickstart', slug: 'quickstart' },
        {
          label: 'Installation',
          items: [
            { label: 'Helm', slug: 'installation/helm' },
            { label: 'Configuration reference', slug: 'installation/configuration' },
          ],
        },
        {
          label: 'Guides',
          items: [
            { label: 'Sending data', slug: 'guides/sending-data' },
            { label: 'Multi-tenancy', slug: 'guides/multi-tenancy' },
            { label: 'Pipelines', slug: 'guides/pipelines' },
            { label: 'Pipeline templates', slug: 'guides/pipeline-templates' },
            { label: 'Metered billing', slug: 'guides/billing' },
            { label: 'Alerting & notifications', slug: 'guides/alerting' },
            { label: 'Tenant self-service portal', slug: 'guides/tenant-portal' },
            { label: 'Edge agents', slug: 'guides/edge-agents' },
            { label: 'Single sign-on', slug: 'guides/sso' },
            { label: 'SAML SSO', slug: 'guides/saml' },
            { label: 'SCIM provisioning', slug: 'guides/scim' },
            { label: 'CLI & config-as-code', slug: 'guides/cli' },
          ],
        },
        { label: 'Architecture', slug: 'architecture' },
        {
          label: 'Operations',
          items: [
            { label: 'Sizing & capacity', slug: 'operations/sizing' },
            { label: 'Runbooks', slug: 'operations/runbooks' },
            { label: 'Security model', slug: 'operations/security-model' },
          ],
        },
        {
          label: 'Reference',
          items: [{ label: 'REST API', slug: 'reference/api' }],
        },
        { label: 'Development', slug: 'development' },
      ],
    }),
  ],
});
