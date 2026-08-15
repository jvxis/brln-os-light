#!/usr/bin/env bash

# Re-enter an installer from the latest published LightningOS release. This
# keeps the familiar ./install*.sh entry points while leaving an explicit
# escape hatch for development and offline installs from the current checkout.
lightningos_bootstrap_latest_release() {
  local installer="$1"
  shift

  local install_source="${LIGHTNINGOS_INSTALL_SOURCE:-latest}"
  case "$install_source" in
    latest) ;;
    checkout) return 0 ;;
    *)
      echo "[ERROR] Invalid LIGHTNINGOS_INSTALL_SOURCE: $install_source (use latest or checkout)." >&2
      exit 1
      ;;
  esac

  if [[ "${LIGHTNINGOS_RELEASE_BOOTSTRAPPED:-0}" == "1" ]]; then
    return 0
  fi

  local project_root
  local bootstrap
  local git_user
  local git_uid
  local origin
  local normalized_origin
  local project_uid
  project_root="$(cd "$REPO_ROOT/.." && pwd)"
  bootstrap="$project_root/lo_bootstrap.sh"
  git_user="${SUDO_USER:-}"

  if [[ ! -d "$project_root/.git" || ! -f "$bootstrap" ]]; then
    echo "[ERROR] Latest-release bootstrap requires the official Git checkout and lo_bootstrap.sh." >&2
    echo "[ERROR] Use lo_bootstrap.sh for a normal install, or explicitly set LIGHTNINGOS_INSTALL_SOURCE=checkout for an offline/development checkout." >&2
    return 1
  fi

  project_uid="$(stat -c '%u' "$project_root" 2>/dev/null || true)"
  git_uid="$(id -u "$git_user" 2>/dev/null || true)"
  if [[ "$(id -u)" -eq 0 && -n "$git_user" && -n "$git_uid" && "$project_uid" == "$git_uid" ]]; then
    origin="$(sudo -H -u "$git_user" git -C "$project_root" remote get-url origin 2>/dev/null || true)"
  else
    git_user=""
    origin="$(git -c safe.directory="$project_root" -C "$project_root" remote get-url origin 2>/dev/null || true)"
  fi
  normalized_origin="${origin%.git}"
  normalized_origin="${normalized_origin%/}"
  case "$normalized_origin" in
    https://github.com/jvxis/brln-os-light|git@github.com:jvxis/brln-os-light|ssh://git@github.com/jvxis/brln-os-light) ;;
    *)
      echo "[ERROR] Latest-release bootstrap requires the official jvxis/brln-os-light Git origin." >&2
      echo "[ERROR] Fix the origin, use lo_bootstrap.sh, or explicitly set LIGHTNINGOS_INSTALL_SOURCE=checkout for an offline/development checkout." >&2
      return 1
      ;;
  esac

  echo "==> Resolving the latest published LightningOS release"
  exec env \
    REPO_URL="$origin" \
    BRLN_DIR="$project_root" \
    BRLN_GIT_USER="$git_user" \
    BRLN_INSTALLER="$installer" \
    LIGHTNINGOS_RELEASE_BOOTSTRAPPED=1 \
    bash "$bootstrap" "$@"
}
