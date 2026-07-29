{{- define "otel-fleet-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "otel-fleet-agent.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "otel-fleet-agent.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
app.kubernetes.io/name: {{ include "otel-fleet-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.Version | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: edge-agent
{{- with .Values.region }}
otel-fleet.io/region: {{ . | quote }}
{{- end }}
{{- with .Values.podLabels }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{- define "otel-fleet-agent.selector" -}}
app.kubernetes.io/name: {{ include "otel-fleet-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "otel-fleet-agent.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.Version) -}}
{{- end -}}

{{/* Secret name holding the bootstrap token (existing or chart-managed). */}}
{{- define "otel-fleet-agent.tokenSecret" -}}
{{- if .Values.bootstrapToken.existingSecret -}}
{{- .Values.bootstrapToken.existingSecret -}}
{{- else -}}
{{- include "otel-fleet-agent.fullname" . -}}
{{- end -}}
{{- end -}}
