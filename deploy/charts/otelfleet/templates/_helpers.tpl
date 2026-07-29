{{- define "otelfleet.name" -}}
{{- .Chart.Name -}}
{{- end -}}

{{- define "otelfleet.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "otelfleet.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
app.kubernetes.io/name: {{ include "otelfleet.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.podLabels }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{- define "otelfleet.selector" -}}
app.kubernetes.io/name: {{ include "otelfleet.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "otelfleet.controlPlaneImage" -}}
{{- printf "%s:%s" .Values.images.controlPlane.repository (.Values.images.controlPlane.tag | default .Chart.AppVersion) -}}
{{- end -}}

{{- define "otelfleet.collectorImage" -}}
{{- printf "%s:%s" .Values.images.collector.repository (.Values.images.collector.tag | default .Chart.AppVersion) -}}
{{- end -}}

{{/* Service selector component for the API tier (http/grpc/ops). */}}
{{- define "otelfleet.apiComponent" -}}
{{- if eq .Values.controlPlane.mode "split" }}api{{ else }}control-plane{{ end -}}
{{- end -}}

{{/* Service selector component for the OpAMP tier. */}}
{{- define "otelfleet.opampComponent" -}}
{{- if eq .Values.controlPlane.mode "split" }}opamp{{ else }}control-plane{{ end -}}
{{- end -}}

{{/* Shared control-plane container env; role is prepended by the caller. */}}
{{- define "otelfleet.controlPlaneEnv" -}}
{{- if .Values.external.databaseUrlSecret }}
- name: OTELFLEET_DATABASE_URL
  valueFrom:
    secretKeyRef: { name: {{ .Values.external.databaseUrlSecret | quote }}, key: OTELFLEET_DATABASE_URL }
{{- else }}
- { name: OTELFLEET_DATABASE_URL, value: {{ required "external.databaseUrl or external.databaseUrlSecret is required" .Values.external.databaseUrl | quote }} }
{{- end }}
- { name: OTELFLEET_CLICKHOUSE_ADDR, value: {{ .Values.external.clickhouse.addr | quote }} }
- { name: OTELFLEET_CLICKHOUSE_DATABASE, value: {{ .Values.external.clickhouse.database | quote }} }
- { name: OTELFLEET_CLICKHOUSE_USER, value: {{ .Values.external.clickhouse.user | quote }} }
{{- if .Values.external.clickhouse.passwordSecret }}
- name: OTELFLEET_CLICKHOUSE_PASSWORD
  valueFrom:
    secretKeyRef: { name: {{ .Values.external.clickhouse.passwordSecret | quote }}, key: OTELFLEET_CLICKHOUSE_PASSWORD }
{{- else }}
- { name: OTELFLEET_CLICKHOUSE_PASSWORD, value: {{ .Values.external.clickhouse.password | quote }} }
{{- end }}
- { name: OTELFLEET_VICTORIAMETRICS_URL, value: {{ .Values.external.victoriaMetrics.url | quote }} }
- { name: OTELFLEET_DEV_LOGIN, value: {{ .Values.controlPlane.devLogin | quote }} }
- { name: OTELFLEET_SESSION_SECURE, value: {{ .Values.controlPlane.sessionSecure | quote }} }
- { name: OTELFLEET_ADMIN_EMAILS, value: {{ join "," .Values.controlPlane.adminEmails | quote }} }
- { name: OTELFLEET_DISTRIBUTOR, value: {{ .Values.controlPlane.distributor | quote }} }
{{- if eq .Values.controlPlane.distributor "k8s" }}
- { name: OTELFLEET_K8S_CR_NAME, value: {{ printf "%s-forwarding" (include "otelfleet.fullname" .) | quote }} }
- { name: OTELFLEET_K8S_CR_NAMESPACE, value: {{ .Release.Namespace | quote }} }
{{- end }}
{{- with .Values.controlPlane.baseUrl }}
- { name: OTELFLEET_BASE_URL, value: {{ . | quote }} }
{{- end }}
{{- with .Values.controlPlane.opamp.publicEndpoint }}
- { name: OTELFLEET_OPAMP_PUBLIC_ENDPOINT, value: {{ . | quote }} }
{{- end }}
{{- with .Values.controlPlane.rateLimit }}
{{- if hasKey . "enabled" }}
- { name: OTELFLEET_RATE_LIMIT_ENABLED, value: {{ .enabled | quote }} }
{{- end }}
{{- if hasKey . "rps" }}
- { name: OTELFLEET_RATE_LIMIT_RPS, value: {{ .rps | quote }} }
{{- end }}
{{- if hasKey . "burst" }}
- { name: OTELFLEET_RATE_LIMIT_BURST, value: {{ .burst | quote }} }
{{- end }}
{{- if hasKey . "authRps" }}
- { name: OTELFLEET_AUTH_RATE_LIMIT_RPS, value: {{ .authRps | quote }} }
{{- end }}
{{- if hasKey . "authBurst" }}
- { name: OTELFLEET_AUTH_RATE_LIMIT_BURST, value: {{ .authBurst | quote }} }
{{- end }}
{{- if hasKey . "maxRequestBodyBytes" }}
- { name: OTELFLEET_MAX_REQUEST_BODY_BYTES, value: {{ .maxRequestBodyBytes | quote }} }
{{- end }}
{{- end }}
{{- with .Values.controlPlane.masterKeySecret }}
- name: OTELFLEET_MASTER_KEY
  valueFrom:
    secretKeyRef: { name: {{ . | quote }}, key: OTELFLEET_MASTER_KEY }
{{- end }}
{{- with .Values.controlPlane.extraEnv }}
{{- toYaml . | nindent 0 }}
{{- end }}
{{- if .Values.controlPlane.oidc.issuer }}
- { name: OTELFLEET_OIDC_ISSUER, value: {{ .Values.controlPlane.oidc.issuer | quote }} }
- { name: OTELFLEET_OIDC_CLIENT_ID, value: {{ .Values.controlPlane.oidc.clientId | quote }} }
- { name: OTELFLEET_OIDC_NAME, value: {{ .Values.controlPlane.oidc.displayName | quote }} }
{{- with .Values.controlPlane.oidc.clientSecretRef }}
- name: OTELFLEET_OIDC_CLIENT_SECRET
  valueFrom:
    secretKeyRef: { name: {{ . | quote }}, key: OTELFLEET_OIDC_CLIENT_SECRET }
{{- end }}
{{- end }}
{{- if .Values.controlPlane.tls.enabled }}
{{- if .Values.controlPlane.tls.publicSecretName }}
- { name: OTELFLEET_TLS_CERT_FILE, value: /etc/otelfleet/tls/tls.crt }
- { name: OTELFLEET_TLS_KEY_FILE, value: /etc/otelfleet/tls/tls.key }
{{- end }}
{{- if .Values.controlPlane.tls.grpcSecretName }}
- { name: OTELFLEET_GRPC_TLS_CERT_FILE, value: /etc/otelfleet/grpc-tls/tls.crt }
- { name: OTELFLEET_GRPC_TLS_KEY_FILE, value: /etc/otelfleet/grpc-tls/tls.key }
{{- if .Values.controlPlane.tls.grpcMTLS }}
- { name: OTELFLEET_GRPC_CLIENT_CA_FILE, value: /etc/otelfleet/grpc-tls/ca.crt }
{{- end }}
{{- end }}
{{- if .Values.controlPlane.tls.opampClientCASecret }}
- { name: OTELFLEET_OPAMP_CLIENT_CA_FILE, value: /etc/otelfleet/opamp-client-ca/ca.crt }
{{- end }}
{{- end }}
# The image bundles the web UI and the collector binary for `otelcol validate`.
- { name: OTELFLEET_WEB_DIR, value: /srv/otelfleet/web }
- { name: OTELFLEET_OTELCOL_BIN, value: /usr/local/bin/otelfleet-collector }
{{- end -}}

{{/* TLS secret volumeMounts for the control-plane container. */}}
{{- define "otelfleet.tlsVolumeMounts" -}}
{{- if .Values.controlPlane.tls.enabled }}
{{- with .Values.controlPlane.tls.publicSecretName }}
- { name: public-tls, mountPath: /etc/otelfleet/tls, readOnly: true }
{{- end }}
{{- with .Values.controlPlane.tls.grpcSecretName }}
- { name: grpc-tls, mountPath: /etc/otelfleet/grpc-tls, readOnly: true }
{{- end }}
{{- with .Values.controlPlane.tls.opampClientCASecret }}
- { name: opamp-client-ca, mountPath: /etc/otelfleet/opamp-client-ca, readOnly: true }
{{- end }}
{{- end }}
{{- end -}}

{{/* TLS secret volumes for the control-plane pod. */}}
{{- define "otelfleet.tlsVolumes" -}}
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
