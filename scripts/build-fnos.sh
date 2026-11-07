#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARCH=amd64
VERSION=1.0.0-beta
CORE=""
CORE_VERSION=bundled
FNPACK="${FNPACK_BIN:-$ROOT/build/tools/fnpack}"
STAGE_ONLY=0
SKIP_FRONTEND=0

ensure_fnpack() {
  if [[ -x "$FNPACK" ]]; then
    return 0
  fi
  if command -v fnpack >/dev/null 2>&1; then
    FNPACK="$(command -v fnpack)"
    return 0
  fi

  printf '未检测到 fnpack 打包环境，正在尝试自动补全下载...\n'
  local os_type arch_type
  case "$(uname -s)" in
    Linux*)  os_type="linux";;
    Darwin*) os_type="darwin";;
    *) printf '不支持自动下载该系统平台的 fnpack: %s\n' "$(uname -s)" >&2; return 1;;
  esac

  case "$(uname -m)" in
    x86_64|amd64)  arch_type="amd64";;
    aarch64|arm64) arch_type="arm64";;
    *) printf '不支持自动下载该架构的 fnpack: %s\n' "$(uname -m)" >&2; return 1;;
  esac

  mkdir -p "$(dirname "$FNPACK")"
  local versions=("1.2.3" "1.2.1" "1.2.0")
  local downloaded=0

  for ver in "${versions[@]}"; do
    local url="https://static2.fnnas.com/fnpack/fnpack-${ver}-${os_type}-${arch_type}"
    printf '尝试下载 %s ...\n' "$url"
    if command -v curl >/dev/null 2>&1; then
      if curl -fsSL "$url" -o "$FNPACK"; then
        downloaded=1
        break
      fi
    elif command -v wget >/dev/null 2>&1; then
      if wget -qO "$FNPACK" "$url"; then
        downloaded=1
        break
      fi
    fi
  done

  if (( ! downloaded )); then
    printf '自动下载 fnpack 失败，请访问 https://developer.fnnas.com/docs/cli/fnpack/ 手动下载并置于 %s\n' "$FNPACK" >&2
    return 1
  fi

  chmod +x "$FNPACK"
  printf 'fnpack 环境就绪: %s\n' "$FNPACK"
}

while (($#)); do
  case "$1" in
    --arch) ARCH="$2"; shift 2;;
    --version) VERSION="$2"; shift 2;;
    --core) CORE="$2"; shift 2;;
    --core-version) CORE_VERSION="$2"; shift 2;;
    --fnpack) FNPACK="$2"; shift 2;;
    --stage-only) STAGE_ONLY=1; shift;;
    --skip-frontend) SKIP_FRONTEND=1; shift;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 1;;
  esac
done

case "$ARCH" in amd64) PLATFORM=x86;; arm64) PLATFORM=arm;; *) printf 'Supported architectures: amd64, arm64\n' >&2; exit 1;; esac
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]]; then printf 'Invalid version\n' >&2; exit 1; fi

STAGE="$ROOT/build/loliafrp-$VERSION-$ARCH"
mkdir -p "$ROOT/build"
rm -rf "$STAGE"
cp -R "$ROOT/packaging/fnos" "$STAGE"
mkdir -p "$STAGE/app/bin" "$STAGE/app/web"

if (( ! SKIP_FRONTEND )); then
  if [[ ! -d "$ROOT/frontend/node_modules" ]]; then
    (cd "$ROOT/frontend" && npm ci)
  fi
  (cd "$ROOT/frontend" && npm run build)
fi

cp -R "$ROOT/frontend/dist/." "$STAGE/app/web/"
(cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -ldflags="-s -w -X github.com/LoliaFRP/loliafrp-fnos/backend/internal/app.Version=$VERSION" -o "$STAGE/app/bin/loliafrp-manager" ./backend/cmd/loliafrp-manager)

if [[ -n "$CORE" ]]; then
  cp "$CORE" "$STAGE/app/bin/frpc"
  printf '%s\n' "$CORE_VERSION" > "$STAGE/app/bin/frpc.version"
  chmod 755 "$STAGE/app/bin/frpc"
fi

sed -i "s/^version=.*/version=$VERSION/" "$STAGE/manifest"
sed -i "s/^platform=.*/platform=$PLATFORM/" "$STAGE/manifest"

cp "$ROOT/LICENSE" "$STAGE/app/"
if [[ -f "$ROOT/NOTICE" ]]; then
  cp "$ROOT/NOTICE" "$STAGE/app/"
fi
chmod 755 "$STAGE"/cmd/* "$STAGE/app/bin/loliafrp-manager"

if (( ! STAGE_ONLY )); then
  ensure_fnpack
  (cd "$ROOT/build" && "$FNPACK" build --directory "$STAGE")
  mv "$ROOT/build/loliafrp.fpk" "$ROOT/build/loliafrp-$VERSION-$ARCH.fpk"
  printf 'Package: %s\n' "$ROOT/build/loliafrp-$VERSION-$ARCH.fpk"
fi
printf 'Staged app: %s\n' "$STAGE"
