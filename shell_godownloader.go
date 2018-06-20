package main

import (
	"fmt"
)

func processGodownloader(repo, path, filename string) ([]byte, error) {
	cfg, err := Load(repo, path, filename)
	if err != nil {
		return nil, fmt.Errorf("unable to parse: %s", err)
	}
	// get archive name template
	archName, err := makeName("NAME=", cfg.Archive.NameTemplate)
	cfg.Archive.NameTemplate = archName
	if err != nil {
		return nil, fmt.Errorf("unable generate archive name: %s", err)
	}
	// get checksum name template
	checkName, err := makeName("CHECKSUM=", cfg.Checksum.NameTemplate)
	cfg.Checksum.NameTemplate = checkName
	if err != nil {
		return nil, fmt.Errorf("unable generate checksum name: %s", err)
	}

	return makeShell(shellGodownloader, cfg)
}

var shellGodownloader = `#!/bin/sh
set -e
# Code generated by godownloader on {{ timestamp }}. DO NOT EDIT.
#

usage() {
  this=$1
  cat <<EOF
$this: download go binaries for {{ $.Release.GitHub.Owner }}/{{ $.Release.GitHub.Name }}

Usage: $this [-b] bindir [-d] [tag]
  -b sets bindir or installation directory, Defaults to ./bin
  -d turns on debug logging
   [tag] is a tag from
   https://github.com/{{ $.Release.GitHub.Owner }}/{{ $.Release.GitHub.Name }}/releases
   If tag is missing, then the latest will be used.

 Generated by godownloader
  https://github.com/sniperkit/godownloader

EOF
  exit 2
}

parse_args() {
  #BINDIR is ./bin unless set be ENV
  # over-ridden by flag below

  BINDIR=${BINDIR:-./bin}
  while getopts "b:dh?" arg; do
    case "$arg" in
      b) BINDIR="$OPTARG" ;;
      d) log_set_priority 10 ;;
      h | \?) usage "$0" ;;
    esac
  done
  shift $((OPTIND - 1))
  TAG=$1
}
# this function wraps all the destructive operations
# if a curl|bash cuts off the end of the script due to
# network, either nothing will happen or will syntax error
# out preventing half-done work
execute() {
  tmpdir=$(mktmpdir)
  log_debug "downloading files into ${tmpdir}"
  http_download "${tmpdir}/${TARBALL}" "${TARBALL_URL}"
  http_download "${tmpdir}/${CHECKSUM}" "${CHECKSUM_URL}"
  hash_sha256_verify "${tmpdir}/${TARBALL}" "${tmpdir}/${CHECKSUM}"
  {{- if .Archive.WrapInDirectory }}
  srcdir="${tmpdir}/${NAME}"
  rm -rf "${srcdir}"
  {{- else }}
  srcdir="${tmpdir}"
  {{- end }}
  (cd "${tmpdir}" && untar "${TARBALL}")
  install -d "${BINDIR}"
  for binexe in {{ range .Builds }}"{{ .Binary }}" {{ end }}; do
    if [ "$OS" = "windows" ]; then
      binexe="${binexe}.exe"
    fi
    install "${srcdir}/${binexe}" "${BINDIR}/"
    log_info "installed ${BINDIR}/${binexe}"
  done
}
is_supported_platform() {
  platform=$1
  found=1
  case "$platform" in
  {{- range $goos := (index $.Builds 0).Goos }}{{ range $goarch := (index $.Builds 0).Goarch }}
{{ if not (eq $goarch "arm") }}    {{ $goos }}/{{ $goarch }}) found=0 ;;{{ end }}
  {{- end }}{{ end }}
  {{- if (index $.Builds 0).Goarm }}
  {{- range $goos := (index $.Builds 0).Goos }}{{ range $goarch := (index $.Builds 0).Goarch }}{{ range $goarm := (index $.Builds 0).Goarm }}
{{- if eq $goarch "arm" }}
    {{ $goos }}/armv{{ $goarm }}) found=0 ;;
{{- end }}
  {{- end }}{{ end }}{{ end }}
  {{- end }}
  esac
  {{- if (index $.Builds 0).Ignore }}
  case "$platform" in
    {{- range $ignore := (index $.Builds 0).Ignore }}
    {{ $ignore.Goos }}/{{ $ignore.Goarch }}{{ if $ignore.Goarm }}v{{ $ignore.Goarm }}{{ end }}) found=1 ;;{{ end }}
  esac
  {{- end }}
  return $found
}
check_platform() {
  if is_supported_platform "$PLATFORM"; then
    # optional logging goes here
    true
  else
    log_crit "platform $PLATFORM is not supported.  Make sure this script is up-to-date and file request at https://github.com/${PREFIX}/issues/new"
    exit 1
  fi
}
tag_to_version() {
  if [ -z "${TAG}" ]; then
    log_info "checking GitHub for latest tag"
  else
    log_info "checking GitHub for tag '${TAG}'"
  fi
  REALTAG=$(github_release "$OWNER/$REPO" "${TAG}") && true
  if test -z "$REALTAG"; then
    log_crit "unable to find '${TAG}' - use 'latest' or see https://github.com/${PREFIX}/releases for details"
    exit 1
  fi
  # if version starts with 'v', remove it
  TAG="$REALTAG"
  VERSION=${TAG#v}
}
adjust_format() {
  # change format (tar.gz or zip) based on ARCH
  {{- with .Archive.FormatOverrides }}
  case ${ARCH} in
  {{- range . }}
    {{ .Goos }}) FORMAT={{ .Format }} ;;
  esac
  {{- end }}
  {{- end }}
  true
}
adjust_os() {
  # adjust archive name based on OS
  {{- with .Archive.Replacements }}
  case ${OS} in
  {{- range $k, $v := . }}
    {{ $k }}) OS={{ $v }} ;;
  {{- end }}
  esac
  {{- end }}
  true
}
adjust_arch() {
  # adjust archive name based on ARCH
  {{- with .Archive.Replacements }}
  case ${ARCH} in
  {{- range $k, $v := . }}
    {{ $k }}) ARCH={{ $v }} ;;
  {{- end }}
  esac
  {{- end }}
  true
}
` + shellfn + `
PROJECT_NAME="{{ $.ProjectName }}"
OWNER={{ $.Release.GitHub.Owner }}
REPO="{{ $.Release.GitHub.Name }}"
BINARY={{ (index .Builds 0).Binary }}
FORMAT={{ .Archive.Format }}
OS=$(uname_os)
ARCH=$(uname_arch)
PREFIX="$OWNER/$REPO"

# use in logging routines
log_prefix() {
  echo "$PREFIX"
}
PLATFORM="${OS}/${ARCH}"
GITHUB_DOWNLOAD=https://github.com/${OWNER}/${REPO}/releases/download

uname_os_check "$OS"
uname_arch_check "$ARCH"

parse_args "$@"

check_platform

tag_to_version

adjust_format

adjust_os

adjust_arch

log_info "found version: ${VERSION} for ${TAG}/${OS}/${ARCH}"

{{ .Archive.NameTemplate }}
TARBALL=${NAME}.${FORMAT}
TARBALL_URL=${GITHUB_DOWNLOAD}/${TAG}/${TARBALL}
{{ .Checksum.NameTemplate }}
CHECKSUM_URL=${GITHUB_DOWNLOAD}/${TAG}/${CHECKSUM}


execute
`
