{{- define "otel-fleet.name" -}}
{{- .Chart.Name -}}
{{- end -}}

{{- define "otel-fleet.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "otel-fleet.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
app.kubernetes.io/name: {{ include "otel-fleet.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.podLabels }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{- define "otel-fleet.selector" -}}
app.kubernetes.io/name: {{ include "otel-fleet.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "otel-fleet.controlPlaneImage" -}}
{{- printf "%s:%s" .Values.images.controlPlane.repository (.Values.images.controlPlane.tag | default .Chart.Version) -}}
{{- end -}}

{{- define "otel-fleet.collectorImage" -}}
{{- printf "%s:%s" .Values.images.collector.repository (.Values.images.collector.tag | default .Chart.Version) -}}
{{- end -}}

{{/* Service selector component for the API tier (http/grpc/ops). */}}
{{- define "otel-fleet.apiComponent" -}}
{{- if eq .Values.controlPlane.mode "split" }}api{{ else }}control-plane{{ end -}}
{{- end -}}

{{/* Service selector component for the OpAMP tier. */}}
{{- define "otel-fleet.opampComponent" -}}
{{- if eq .Values.controlPlane.mode "split" }}opamp{{ else }}control-plane{{ end -}}
{{- end -}}

{{/* Shared control-plane container env; role is prepended by the caller. */}}
{{- define "otel-fleet.controlPlaneEnv" -}}
{{- if .Values.external.databaseUrlSecret }}
- name: OTEL_FLEET_DATABASE_URL
  valueFrom:
    secretKeyRef: { name: {{ .Values.external.databaseUrlSecret | quote }}, key: OTEL_FLEET_DATABASE_URL }
{{- else }}
- { name: OTEL_FLEET_DATABASE_URL, value: {{ required "external.databaseUrl or external.databaseUrlSecret is required" .Values.external.databaseUrl | quote }} }
{{- end }}
- { name: OTEL_FLEET_CLICKHOUSE_ADDR, value: {{ .Values.external.clickhouse.addr | quote }} }
- { name: OTEL_FLEET_CLICKHOUSE_DATABASE, value: {{ .Values.external.clickhouse.database | quote }} }
- { name: OTEL_FLEET_CLICKHOUSE_USER, value: {{ .Values.external.clickhouse.user | quote }} }
{{- if .Values.external.clickhouse.passwordSecret }}
- name: OTEL_FLEET_CLICKHOUSE_PASSWORD
  valueFrom:
    secretKeyRef: { name: {{ .Values.external.clickhouse.passwordSecret | quote }}, key: OTEL_FLEET_CLICKHOUSE_PASSWORD }
{{- else }}
- { name: OTEL_FLEET_CLICKHOUSE_PASSWORD, value: {{ .Values.external.clickhouse.password | quote }} }
{{- end }}
- { name: OTEL_FLEET_VICTORIAMETRICS_URL, value: {{ .Values.external.victoriaMetrics.url | quote }} }
- { name: OTEL_FLEET_DEV_LOGIN, value: {{ .Values.controlPlane.devLogin | quote }} }
- { name: OTEL_FLEET_SESSION_SECURE, value: {{ .Values.controlPlane.sessionSecure | quote }} }
- { name: OTEL_FLEET_ADMIN_EMAILS, value: {{ join "," .Values.controlPlane.adminEmails | quote }} }
- { name: OTEL_FLEET_DISTRIBUTOR, value: {{ .Values.controlPlane.distributor | quote }} }
{{- if eq .Values.controlPlane.distributor "k8s" }}
- { name: OTEL_FLEET_K8S_CR_NAME, value: {{ printf "%s-forwarding" (include "otel-fleet.fullname" .) | quote }} }
- { name: OTEL_FLEET_K8S_CR_NAMESPACE, value: {{ .Release.Namespace | quote }} }
{{- end }}
{{- with .Values.controlPlane.baseUrl }}
- { name: OTEL_FLEET_BASE_URL, value: {{ . | quote }} }
{{- end }}
{{- with .Values.controlPlane.opamp.publicEndpoint }}
- { name: OTEL_FLEET_OPAMP_PUBLIC_ENDPOINT, value: {{ . | quote }} }
{{- end }}
{{- with .Values.controlPlane.rateLimit }}
{{- if hasKey . "enabled" }}
- { name: OTEL_FLEET_RATE_LIMIT_ENABLED, value: {{ .enabled | quote }} }
{{- end }}
{{- if hasKey . "rps" }}
- { name: OTEL_FLEET_RATE_LIMIT_RPS, value: {{ .rps | quote }} }
{{- end }}
{{- if hasKey . "burst" }}
- { name: OTEL_FLEET_RATE_LIMIT_BURST, value: {{ .burst | quote }} }
{{- end }}
{{- if hasKey . "authRps" }}
- { name: OTEL_FLEET_AUTH_RATE_LIMIT_RPS, value: {{ .authRps | quote }} }
{{- end }}
{{- if hasKey . "authBurst" }}
- { name: OTEL_FLEET_AUTH_RATE_LIMIT_BURST, value: {{ .authBurst | quote }} }
{{- end }}
{{- if hasKey . "maxRequestBodyBytes" }}
- { name: OTEL_FLEET_MAX_REQUEST_BODY_BYTES, value: {{ .maxRequestBodyBytes | quote }} }
{{- end }}
{{- end }}
{{- with .Values.controlPlane.masterKeySecret }}
- name: OTEL_FLEET_MASTER_KEY
  valueFrom:
    secretKeyRef: { name: {{ . | quote }}, key: OTEL_FLEET_MASTER_KEY }
{{- end }}
{{- with .Values.controlPlane.extraEnv }}
{{- toYaml . | nindent 0 }}
{{- end }}
{{- if .Values.controlPlane.oidc.issuer }}
- { name: OTEL_FLEET_OIDC_ISSUER, value: {{ .Values.controlPlane.oidc.issuer | quote }} }
- { name: OTEL_FLEET_OIDC_CLIENT_ID, value: {{ .Values.controlPlane.oidc.clientId | quote }} }
- { name: OTEL_FLEET_OIDC_NAME, value: {{ .Values.controlPlane.oidc.displayName | quote }} }
{{- with .Values.controlPlane.oidc.clientSecretRef }}
- name: OTEL_FLEET_OIDC_CLIENT_SECRET
  valueFrom:
    secretKeyRef: { name: {{ . | quote }}, key: OTEL_FLEET_OIDC_CLIENT_SECRET }
{{- end }}
{{- end }}
{{- if .Values.controlPlane.tls.enabled }}
{{- if .Values.controlPlane.tls.publicSecretName }}
- { name: OTEL_FLEET_TLS_CERT_FILE, value: /etc/otel-fleet/tls/tls.crt }
- { name: OTEL_FLEET_TLS_KEY_FILE, value: /etc/otel-fleet/tls/tls.key }
{{- end }}
{{- if .Values.controlPlane.tls.grpcSecretName }}
- { name: OTEL_FLEET_GRPC_TLS_CERT_FILE, value: /etc/otel-fleet/grpc-tls/tls.crt }
- { name: OTEL_FLEET_GRPC_TLS_KEY_FILE, value: /etc/otel-fleet/grpc-tls/tls.key }
{{- if .Values.controlPlane.tls.grpcMTLS }}
- { name: OTEL_FLEET_GRPC_CLIENT_CA_FILE, value: /etc/otel-fleet/grpc-tls/ca.crt }
{{- end }}
{{- end }}
{{- if .Values.controlPlane.tls.opampClientCASecret }}
- { name: OTEL_FLEET_OPAMP_CLIENT_CA_FILE, value: /etc/otel-fleet/opamp-client-ca/ca.crt }
{{- end }}
{{- end }}
# The image bundles the web UI and the collector binary for `otelcol validate`.
- { name: OTEL_FLEET_WEB_DIR, value: /srv/otel-fleet/web }
- { name: OTEL_FLEET_OTELCOL_BIN, value: /usr/local/bin/otel-fleet-collector }
{{- end -}}

{{/* TLS secret volumeMounts for the control-plane container. */}}
{{- define "otel-fleet.tlsVolumeMounts" -}}
{{- if .Values.controlPlane.tls.enabled }}
{{- with .Values.controlPlane.tls.publicSecretName }}
- { name: public-tls, mountPath: /etc/otel-fleet/tls, readOnly: true }
{{- end }}
{{- with .Values.controlPlane.tls.grpcSecretName }}
- { name: grpc-tls, mountPath: /etc/otel-fleet/grpc-tls, readOnly: true }
{{- end }}
{{- with .Values.controlPlane.tls.opampClientCASecret }}
- { name: opamp-client-ca, mountPath: /etc/otel-fleet/opamp-client-ca, readOnly: true }
{{- end }}
{{- end }}
{{- end -}}

{{/* TLS secret volumes for the control-plane pod. */}}
{{- define "otel-fleet.tlsVolumes" -}}
{{- if .Values.controlPlane.tls.enabled }}
{{- with .Values.controlPlane.tls.publicSecretName }}
- { name: public-tls, secret: { secretName: {{ . | quote }} } }
{{- end }}
{{- with .Values.controlPlane.tls.grpcSecretName }}
- { name: grpc-tls, secret: { secretName: {{ . | quote }} } }
{{- end }}
{{- with .Values.controlPlane.tls.opampClientCASecret }}
- { name: opamp-client-ca, secret: { secretName: {{ . | quote }} } }
{{- end }}
{{- end }}
{{- end -}}
