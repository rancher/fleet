#!/bin/sh
set -e

GITHUB_URL=https://github.com/rancher/fleet/releases

SHA="sha256sum"
if [ "$(uname)" = "Darwin" ]; then
    SHA="shasum -a 256"
fi

# --- helper functions for logs ---
info()
{
    echo "[INFO] " "$@"
}
fatal()
{
    echo "[ERROR] " "$@"
    exit 1
}


# --- define needed environment variables ---
setup_env() {
    # --- use sudo if we are not already root ---
    SUDO=sudo
    if [ -n "${SKIP_SUDO}" ] || [ "$(id -u)" = 0 ]; then
        SUDO=
    fi

    # --- use binary install directory if defined or create default ---
    if [ -n "${INSTALL_FLEET_BIN_DIR}" ]; then
        BIN_DIR="${INSTALL_FLEET_BIN_DIR}"
    else
        BIN_DIR="/usr/local/bin"
    fi

    # --- get hash of config & exec for currently installed fleet ---
    PRE_INSTALL_HASHES=$(get_installed_hashes)
    export PRE_INSTALL_HASHES
}

# --- set arch and suffix, fatal if architecture not supported ---
setup_verify_arch() {
    OS=$(echo "$(uname)"|tr '[:upper:]' '[:lower:]')

    case "$OS" in
        # Minimalist GNU for Windows
        mingw*) OS='windows';;
        linux)
            ;;
        windows)
            ;;
        darwin)
            ;;
        *)
            fatal "Unsupported OS $OS"
    esac

    if [ -z "$ARCH" ]; then
        ARCH=$(uname -m)
    fi
    case $ARCH in
        amd64)
            ARCH=amd64
            ;;
        x86_64)
            ARCH=amd64
            ;;
        arm64)
            ARCH=arm64
            ;;
        aarch64)
            ARCH=arm64
            ;;
        arm*)
            ARCH=arm
            ;;
        *)
            fatal "Unsupported architecture $ARCH"
    esac

    SUFFIX=-${OS}-${ARCH}
}

# --- fatal if no curl ---
verify_curl() {
    if [ -z "$(command -v curl)" ]; then
        fatal "Can not find curl for downloading files"
    fi
}

# --- create tempory directory and cleanup when done ---
setup_tmp() {
    TMP_DIR=$(mktemp -d -t fleet-install.XXXXXXXXXX)
    TMP_HASH=${TMP_DIR}/fleet.hash
    TMP_BIN=${TMP_DIR}/fleet.bin
    cleanup() {
        code=$?
        set +e
        trap - EXIT
        rm -rf "${TMP_DIR}"
        exit $code
    }
    trap cleanup INT EXIT
}

# --- use desired fleet version if defined or find latest ---
get_release_version() {
    if [ -n "${INSTALL_FLEET_VERSION}" ]; then
        VERSION_FLEET="${INSTALL_FLEET_VERSION}"
    else
        info "Finding latest release"
        VERSION_FLEET=$(curl -w "%{url_effective}" -I -L -s -S ${GITHUB_URL}/latest -o /dev/null | sed -e 's|.*/||')
    fi
    info "Using ${VERSION_FLEET} as release"
}

# --- download hash from github url ---
download_hash() {
    HASH_URL=${GITHUB_URL}/download/${VERSION_FLEET}/sha256sum-${ARCH}.txt
    info "Downloading hash ${HASH_URL}"
    curl -o "${TMP_HASH}" -sfL "${HASH_URL}" || fatal "Hash download failed"
    HASH_EXPECTED=$(grep " flt${SUFFIX}$" "${TMP_HASH}" | awk '{print $1}')
}

# --- check hash against installed version ---
installed_hash_matches() {
    if [ -x ${BIN_DIR}/flt ]; then
        HASH_INSTALLED=$($SHA ${BIN_DIR}/flt | awk '{print $1}')
        if [ "${HASH_EXPECTED}" = "${HASH_INSTALLED}" ]; then
            return
        fi
    fi
    return 1
}

# --- download binary from github url ---
download_binary() {
    BIN_URL=${GITHUB_URL}/download/${VERSION_FLEET}/flt${SUFFIX}
    info "Downloading binary ${BIN_URL}"
    curl -o "${TMP_BIN}" -fL "${BIN_URL}" || fatal "Binary download failed"
}

# --- verify downloaded binary hash ---
verify_binary() {
    info "Verifying binary download"
    HASH_BIN=$($SHA "${TMP_BIN}" | awk '{print $1}')
    if [ "${HASH_EXPECTED}" != "${HASH_BIN}" ]; then
        fatal "Download sha256 does not match ${HASH_EXPECTED}, got ${HASH_BIN}"
    fi
}

# --- setup permissions and move binary to system directory ---
setup_binary() {
    chmod 755 "${TMP_BIN}"
    info "Installing fleet to ${BIN_DIR}/flt"
    [ -n "$SUDO" ] && { $SUDO chown 0:0 "${TMP_BIN}"; }
    $SUDO mv -f "${TMP_BIN}" ${BIN_DIR}/flt

    if command -v getenforce > /dev/null 2>&1; then
        if [ "Disabled" != "$(getenforce)" ]; then
            info "SeLinux is enabled, setting permissions"
            if ! $SUDO semanage fcontext -l | grep "${BIN_DIR}/flt" > /dev/null 2>&1; then
                $SUDO semanage fcontext -a -t bin_t "${BIN_DIR}/flt"
            fi
            $SUDO restorecon -v ${BIN_DIR}/flt > /dev/null
        fi
    fi
}

# --- download and verify fleet ---
download_and_verify() {
    setup_verify_arch
    verify_curl
    setup_tmp
    get_release_version
    download_hash

    if installed_hash_matches; then
        info "Skipping binary downloaded, installed fleet matches hash"
        return
    fi

    download_binary
    verify_binary
    setup_binary
}

# --- create uninstall script ---
create_uninstall() {
    [ "${INSTALL_FLEET_BIN_DIR_READ_ONLY}" = "true" ] && return
    info "Creating uninstall script ${BIN_DIR}/${UNINSTALL_FLEET_SH}"
    $SUDO tee ${BIN_DIR}/${UNINSTALL_FLEET_SH} >/dev/null << EOF
#!/bin/sh
set -x
[ \`id -u\` = 0 ] || exec sudo \$0 \$@

remove_uninstall() {
    rm -f ${BIN_DIR}/${UNINSTALL_FLEET_SH}
}
trap remove_uninstall EXIT
EOF
    $SUDO chmod 755 ${BIN_DIR}/${UNINSTALL_FLEET_SH}
    [ -n "$SUDO" ] && { $SUDO ${BIN_DIR}/${UNINSTALL_FLEET_SH}; }
}

# --- get hashes of the current fleet bin and service files
get_installed_hashes() {
    $SUDO "$SHA" ${BIN_DIR}/flt 2>&1 || true
}

# --- run the install process --
{
    setup_env
    download_and_verify
    #create_uninstall
}
