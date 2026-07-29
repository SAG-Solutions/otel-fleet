// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
  site: 'https://sag-solutions.github.io',
  base: '/otel-fleet',
  integrations: [
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
          label: 'Reference',
          items: [{ label: 'REST API', slug: 'reference/api' }],
        },
        { label: 'Development', slug: 'development' },
      ],
    }),
  ],
});
