#!/usr/bin/env bash
# wayneblacktea install script (macOS / Linux / WSL)
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Wayne997035/wayneblacktea/master/scripts/install.sh | bash
#
# IMPORTANT: when piping to bash there is NO interactive wizard — stdin is
# the curl process, not your terminal. The script writes an EMPTY API_KEY=
# line and you MUST edit ~/.config/wayneblacktea/.env before starting the
# server. To use the wizard, save the script first then run it directly.
#
# Environment overrides:
#   WBT_VERSION              Pin to a specific release (default: latest)
#   WBT_PREFIX               Install prefix (default: $HOME/.local)
#   WBT_CONFIG               Config dir (default: $HOME/.config/wayneblacktea)
#   WBT_NO_MCP               If set to "1", skip `claude mcp add` registration
#   WBT_NO_PROMPT            If set to "1", force non-interactive (placeholders, empty API_KEY)
#   WBT_INSECURE_SKIP_VERIFY If set to "1", skip cosign verification (NOT RECOMMENDED).
#                            Default behaviour is fail-closed: cosign + sig + cert MUST
#                            all be present and the signature MUST verify, otherwise
#                            installation aborts. (PR #85 R3 / S-N1)
#
# Exit codes:
#   0  success
#   1  user-facing error (printed to stderr)
#   2  download / checksum / signature verification failure

set -euo pipefail

REPO_OWNER="Wayne997035"
REPO_NAME="wayneblacktea"
GITHUB_API="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest"
GITHUB_DL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download"

# cosign keyless verification — sigstore certificate identity / OIDC issuer
# pinned to wayneblacktea repo + GitHub Actions OIDC issuer used by goreleaser.
# Anchored regex (^...$) to prevent prefix/suffix injection attacks; restricts
# to the release.yml workflow on a semver tag. (PR #85 R3 / S-N2)
COSIGN_CERT_IDENTITY_REGEX='^https://github\.com/Wayne997035/wayneblacktea/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$'
COSIGN_OIDC_ISSUER='https://token.actions.githubusercontent.com'

PREFIX="${WBT_PREFIX:-${HOME}/.local}"
BIN_DIR="${PREFIX}/bin"
CONFIG_DIR="${WBT_CONFIG:-${HOME}/.config/wayneblacktea}"
ENV_FILE="${CONFIG_DIR}/.env"

# Binaries shipped in the cli archive (must stay in sync with .goreleaser.yaml
# archive id "cli" `ids:` list).
CLI_BINARIES=(wbt wbt-context wbt-hook wbt-guard wbt-doctor)
MCP_BINARY="wayneblacktea-mcp"

# --- log helpers (stderr; stdout reserved for piped data only) ---
log_info()  { printf '\033[1;34m[install]\033[0m %s\n' "$*" >&2; }
log_warn()  { printf '\033[1;33m[install]\033[0m WARN: %s\n' "$*" >&2; }
log_error() { printf '\033[1;31m[install]\033[0m ERROR: %s\n' "$*" >&2; }
die()       { log_error "$*"; exit 1; }

# --- temp dir + cleanup ---
TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t 'wbt-install')"
cleanup() { rm -rf "${TMP_DIR}"; }
trap cleanup EXIT INT TERM

# --- prerequisite tooling ---
need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1 (install it and rerun)"
}
need_cmd curl
need_cmd tar
need_cmd uname

# sha256 verification: prefer sha256sum, fall back to shasum (macOS default)
sha256_check() {
  local file="$1" expected="$2" actual=""
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "${file}" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "${file}" | awk '{print $1}')"
  else
    die "missing sha256sum/shasum — cannot verify download integrity"
  fi
  if [ "${actual}" != "${expected}" ]; then
    log_error "checksum mismatch for ${file}"
    log_error "  expected: ${expected}"
    log_error "  actual:   ${actual}"
    exit 2
  fi
}

# verify_cosign verifies checksums.txt authenticity using cosign keyless mode
# against the signature published by goreleaser.
#
# DEFAULT: fail-closed. If cosign is missing OR the signature/cert artefacts
# are missing OR verification fails, the installer aborts. This prevents an
# attacker who can MITM (or 404-spoof) the .sig/.pem from forcing a
# warn-and-proceed code path.
#
# Escape hatch (NOT RECOMMENDED): set WBT_INSECURE_SKIP_VERIFY=1 to skip the
# entire signature step. (PR #85 R3 / S-N1)
verify_cosign() {
  local checksums_path="$1" sig_path="$2" cert_path="$3"

  if [ "${WBT_INSECURE_SKIP_VERIFY:-0}" = "1" ]; then
    log_warn "WBT_INSECURE_SKIP_VERIFY=1 — skipping cosign verification (NOT RECOMMENDED for production installs)"
    return 0
  fi

  if ! command -v cosign >/dev/null 2>&1; then
    die "cosign not installed — install cosign or set WBT_INSECURE_SKIP_VERIFY=1 to bypass (not recommended). https://docs.sigstore.dev/system_config/installation/"
  fi

  if [ ! -s "${sig_path}" ]; then
    die "signature file ${sig_path} missing/empty — refusing to install. Set WBT_INSECURE_SKIP_VERIFY=1 to bypass (NOT RECOMMENDED)."
  fi

  if [ ! -s "${cert_path}" ]; then
    die "certificate file ${cert_path} missing/empty — refusing to install. Set WBT_INSECURE_SKIP_VERIFY=1 to bypass (NOT RECOMMENDED)."
  fi

  log_info "verifying checksums.txt signature with cosign"
  if ! cosign verify-blob \
      --certificate-identity-regexp "${COSIGN_CERT_IDENTITY_REGEX}" \
      --certificate-oidc-issuer "${COSIGN_OIDC_ISSUER}" \
      --certificate "${cert_path}" \
      --signature "${sig_path}" \
      "${checksums_path}" >/dev/null 2>&1; then
    die "cosign verification failed"
  fi
  log_info "cosign signature OK"
}

# verify_archive_safe rejects tar archives containing absolute paths, parent
# traversal segments (`..`), `~`-prefixed entries, AND symlink/hardlink
# entries. Defends against zip-slip (CVE-2018-1002150 family) plus the
# symlink-attack variant where an entry name like `wbt -> /etc/passwd` slips
# past a name-only filter. (PR #85 R3 — reviewer minor)
#
# `tar -tzvf` long listing has the file mode in column 1; `l` = symlink,
# `h` = hardlink. `awk '{print $NF}'` extracts the path field (last column).
verify_archive_safe() {
  local archive="$1"
  local listing
  listing="$(tar -tzvf "${archive}" 2>/dev/null)" || die "cannot read archive ${archive}"
  if printf '%s\n' "${listing}" | grep -qE '^[lh]'; then
    log_error "archive ${archive} contains symlink/hardlink entries — refusing to extract"
    exit 2
  fi
  if printf '%s\n' "${listing}" | awk '{print $NF}' | grep -qE '(^/|(^|/)\.\.($|/)|^~)'; then
    log_error "archive ${archive} contains unsafe paths (absolute / .. / ~) — refusing to extract"
    exit 2
  fi
}

# --- detect OS / arch (matches goreleaser archive naming) ---
detect_platform() {
  local kernel arch os_norm arch_norm
  kernel="$(uname -s)"
  arch="$(uname -m)"

  case "${kernel}" in
    Linux)   os_norm="linux" ;;
    Darwin)  os_norm="macos" ;;
    *)       die "unsupported OS: ${kernel} (supported: Linux, macOS, WSL)" ;;
  esac

  case "${arch}" in
    x86_64|amd64)       arch_norm="x86_64" ;;
    aarch64|arm64)      arch_norm="arm64" ;;
    *)                  die "unsupported architecture: ${arch}" ;;
  esac

  printf '%s %s' "${os_norm}" "${arch_norm}"
}

# --- fetch latest version tag (or use pinned WBT_VERSION) ---
# Validates the resolved version against a strict semver pattern before any
# URL interpolation so a hostile WBT_VERSION value cannot inject path or
# query-string components into the GitHub release URL.
# (PR #85 R3 / S-N3)
fetch_version() {
  local version="${WBT_VERSION:-}"
  if [ -z "${version}" ]; then
    version="$(curl -fsSL --retry 3 --retry-delay 2 "${GITHUB_API}" \
      | grep -E '^\s*"tag_name"\s*:' \
      | head -n1 \
      | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"v?([^"]+)".*/\1/')"
    if [ -z "${version}" ]; then
      die "failed to resolve latest release tag from ${GITHUB_API}"
    fi
  fi
  version="${version#v}"
  if ! printf '%s' "${version}" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$'; then
    die "invalid version format: ${version} (expected MAJOR.MINOR.PATCH[-prerelease])"
  fi
  printf '%s' "${version}"
}

# --- download + verify a single archive ---
# args: <url> <out_file> <checksums_file>
download_and_verify() {
  local url="$1" out="$2" sums="$3" name expected
  name="$(basename "${out}")"
  log_info "downloading ${name}"
  if ! curl -fsSL --retry 3 --retry-delay 2 -o "${out}" "${url}"; then
    log_error "failed to download ${url}"
    exit 2
  fi
  expected="$(awk -v n="${name}" '$2 == n {print $1; exit}' "${sums}")"
  if [ -z "${expected}" ]; then
    log_error "checksum entry for ${name} not found in checksums.txt"
    exit 2
  fi
  sha256_check "${out}" "${expected}"
}

# --- compare semver-ish strings (returns 0 if $1 >= $2) ---
version_ge() {
  # strip leading 'v' for comparison; sort -V handles pre-release ordering loosely
  local a="${1#v}" b="${2#v}"
  [ "$(printf '%s\n%s\n' "${a}" "${b}" | sort -V | tail -n1)" = "${a}" ]
}

# --- check existing wbt and refuse downgrade ---
guard_existing_install() {
  local target="$1" existing
  if ! command -v wbt >/dev/null 2>&1; then
    return
  fi
  # `wbt version` prints "<binary> <version> (<commit>)"; second whitespace
  # token is the version. Falls back to `wbt --version` if the user runs
  # this script against an old binary that didn't ship the subcommand.
  existing="$(wbt version 2>/dev/null | head -n1 | awk '{print $2}' || true)"
  if [ -z "${existing}" ]; then
    existing="$(wbt --version 2>/dev/null | head -n1 | awk '{print $2}' || true)"
  fi
  if [ -z "${existing}" ] || [ "${existing}" = "dev" ]; then
    return
  fi
  if version_ge "${existing}" "${target}" && [ "${existing}" != "${target}" ]; then
    log_warn "existing wbt (${existing}) is newer than target (${target}); aborting"
    log_warn "set WBT_VERSION=${existing} to reinstall, or remove existing binary first"
    exit 1
  fi
}

# --- ensure ~/.local/bin exists and warn if not on PATH ---
ensure_bin_dir() {
  mkdir -p "${BIN_DIR}"
  case ":${PATH}:" in
    *":${BIN_DIR}:"*) return ;;
  esac
  log_warn "${BIN_DIR} is not on your PATH"
  local rc=""
  case "${SHELL:-}" in
    */zsh)  rc="${HOME}/.zshrc" ;;
    */bash) rc="${HOME}/.bashrc" ;;
    */fish) rc="${HOME}/.config/fish/config.fish" ;;
  esac
  if [ -n "${rc}" ]; then
    log_warn "add this line to ${rc} and restart your shell:"
    case "${rc}" in
      */fish/*) log_warn "  fish_add_path ${BIN_DIR}" ;;
      *)        log_warn "  export PATH=\"${BIN_DIR}:\$PATH\"" ;;
    esac
  else
    log_warn "add ${BIN_DIR} to your shell's PATH manually"
  fi
}

# --- write .env interactively (or empty placeholders if WBT_NO_PROMPT=1) ---
write_env_file() {
  if [ -e "${ENV_FILE}" ]; then
    log_info "config already exists at ${ENV_FILE} (skipping wizard)"
    return
  fi
  mkdir -p "${CONFIG_DIR}"
  chmod 700 "${CONFIG_DIR}" 2>/dev/null || true

  local db_url="" api_key="" workspace_id=""

  if [ "${WBT_NO_PROMPT:-0}" = "1" ] || [ ! -t 0 ]; then
    # IMPORTANT: do NOT write a guessable placeholder string here. Earlier
    # versions wrote `REPLACE_ME_RUN_openssl_rand_-hex_32` which is now a
    # known-bad credential — any attacker can try the literal string. Write
    # an empty value so the server fails closed (API_KEY required) and the
    # operator MUST edit the file before it starts. (Round 2 / S-M4)
    log_info "non-interactive install — writing .env with empty API_KEY"
    log_warn "edit ${ENV_FILE} and set API_KEY before starting the server"
    log_warn "  generate a random key with: openssl rand -hex 32"
    db_url=""
    api_key=""
    workspace_id=""
  else
    printf '\n' >&2
    printf 'Enter DATABASE_URL (postgres://... — leave blank for SQLite local file):\n' >&2
    printf '> ' >&2
    IFS= read -r db_url || db_url=""

    while [ -z "${api_key}" ]; do
      printf 'Enter API_KEY (required; suggested: openssl rand -hex 32):\n' >&2
      printf '> ' >&2
      # -s suppresses echo so the secret is not displayed on screen
      # (CWE-200). After the silent read we emit a newline so the cursor
      # moves to the next line. (Round 2 / S-M1)
      IFS= read -rs api_key || api_key=""
      printf '\n' >&2
      if [ -z "${api_key}" ]; then
        log_warn "API_KEY cannot be empty"
      fi
    done

    printf 'Enter WORKSPACE_ID (optional, leave blank to use default):\n' >&2
    printf '> ' >&2
    IFS= read -r workspace_id || workspace_id=""
  fi

  # Write atomically with strict perms BEFORE moving into place
  local tmp_env="${TMP_DIR}/.env.new"
  umask 077
  {
    printf '# wayneblacktea config — managed by install.sh\n'
    printf '# DATABASE_URL — leave blank to use SQLite local file at $HOME/.wayneblacktea/data.db\n'
    printf 'DATABASE_URL=%s\n' "${db_url}"
    printf '# API_KEY — gates every /api/* route; rotate via openssl rand -hex 32\n'
    printf 'API_KEY=%s\n' "${api_key}"
    printf '# WORKSPACE_ID — optional UUID for multi-workspace scoping\n'
    printf 'WORKSPACE_ID=%s\n' "${workspace_id}"
  } > "${tmp_env}"
  mv "${tmp_env}" "${ENV_FILE}"
  chmod 600 "${ENV_FILE}"
  # Clear sensitive value from environment as soon as it has been written.
  api_key=""
  log_info "wrote ${ENV_FILE} (mode 0600)"
}

# --- register MCP server with claude CLI if present ---
register_mcp() {
  if [ "${WBT_NO_MCP:-0}" = "1" ]; then
    log_info "WBT_NO_MCP=1 — skipping claude mcp add"
    return
  fi
  if ! command -v claude >/dev/null 2>&1; then
    log_warn "claude CLI not found — skipping MCP registration"
    log_warn "after installing Claude Code, run:"
    log_warn "  claude mcp add wayneblacktea -- ${BIN_DIR}/${MCP_BINARY}"
    return
  fi
  log_info "registering wayneblacktea MCP server with claude CLI"
  if claude mcp add wayneblacktea -- "${BIN_DIR}/${MCP_BINARY}" 2>/dev/null; then
    log_info "MCP server registered"
  else
    log_warn "claude mcp add failed (already registered?) — verify with: claude mcp list"
  fi
}

# --- main ---
main() {
  local plat os arch version
  plat="$(detect_platform)"
  os="${plat% *}"
  arch="${plat#* }"
  log_info "detected platform: ${os}/${arch}"

  version="$(fetch_version)"
  log_info "installing wayneblacktea v${version}"
  guard_existing_install "${version}"

  local mcp_archive cli_archive checksums_file checksums_sig checksums_cert
  mcp_archive="wayneblacktea-mcp_${version}_${os}_${arch}.tar.gz"
  cli_archive="wayneblacktea-cli_${version}_${os}_${arch}.tar.gz"
  checksums_file="checksums.txt"
  # goreleaser cosign signs/cmd publishes both .sig and .pem (cert).
  checksums_sig="${checksums_file}.sig"
  checksums_cert="${checksums_file}.pem"

  local sums_path="${TMP_DIR}/${checksums_file}"
  local sig_path="${TMP_DIR}/${checksums_sig}"
  local cert_path="${TMP_DIR}/${checksums_cert}"
  log_info "fetching checksums.txt"
  if ! curl -fsSL --retry 3 --retry-delay 2 \
      -o "${sums_path}" \
      "${GITHUB_DL}/v${version}/${checksums_file}"; then
    die "failed to download checksums file from release v${version}"
  fi

  # Best-effort: fetch signature + certificate (some older releases may not
  # have them). cosign verify happens before checksums are parsed.
  curl -fsSL --retry 2 --retry-delay 2 -o "${sig_path}" \
    "${GITHUB_DL}/v${version}/${checksums_sig}" 2>/dev/null || true
  curl -fsSL --retry 2 --retry-delay 2 -o "${cert_path}" \
    "${GITHUB_DL}/v${version}/${checksums_cert}" 2>/dev/null || true

  verify_cosign "${sums_path}" "${sig_path}" "${cert_path}"

  download_and_verify \
    "${GITHUB_DL}/v${version}/${mcp_archive}" \
    "${TMP_DIR}/${mcp_archive}" \
    "${sums_path}"
  download_and_verify \
    "${GITHUB_DL}/v${version}/${cli_archive}" \
    "${TMP_DIR}/${cli_archive}" \
    "${sums_path}"

  log_info "verifying archive contents are safe"
  verify_archive_safe "${TMP_DIR}/${mcp_archive}"
  verify_archive_safe "${TMP_DIR}/${cli_archive}"

  log_info "extracting archives"
  # --no-same-owner / --no-same-permissions ensure the install user owns
  # the extracted files regardless of what the archive metadata claims.
  tar -xzf "${TMP_DIR}/${mcp_archive}" -C "${TMP_DIR}" --no-same-owner --no-same-permissions
  tar -xzf "${TMP_DIR}/${cli_archive}" -C "${TMP_DIR}" --no-same-owner --no-same-permissions

  ensure_bin_dir

  # Install all 6 binaries: wayneblacktea-mcp + 5 CLI tools (wbt, wbt-context,
  # wbt-hook, wbt-guard, wbt-doctor). MUST stay aligned with .goreleaser.yaml
  # archive `cli` ids list. (Round 2 / R-M2)
  local bin
  for bin in "${MCP_BINARY}" "${CLI_BINARIES[@]}"; do
    if [ ! -f "${TMP_DIR}/${bin}" ]; then
      die "expected binary ${bin} missing from extracted archives"
    fi
    install -m 0755 "${TMP_DIR}/${bin}" "${BIN_DIR}/${bin}"
    log_info "installed ${BIN_DIR}/${bin}"
  done

  write_env_file
  register_mcp

  printf '\n' >&2
  log_info "wayneblacktea v${version} installed successfully"
  log_info "binaries:    ${BIN_DIR}"
  log_info "config:      ${ENV_FILE}"
  log_info "next steps:  edit ${ENV_FILE} (set API_KEY first) then run: wbt --help"
}

main "$@"
