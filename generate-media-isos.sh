#!/usr/bin/env bash
# Create full-capacity ISO9660 images containing cryptographically random data.
set -euo pipefail

readonly SECTOR_BYTES=2048 RANDOM_CHUNK_BYTES=$((4 * 1024 * 1024))
# CD-R's 80-minute lead-out begins at LBA 359849. Burners reserve a 150-sector
# (two-second) run-out/lead-out margin, so an ISO image must be no larger than
# 359699 sectors. 360000 sectors is only the encoded running-time capacity.
# Individual media can still be smaller.
declare -Ar MEDIA_BLOCKS=([cd-r]=359699 [dvd-r]=2297888 [bd-r]=12219392)

usage() {
  cat <<'EOF'
Usage: generate-media-isos.sh [options] MEDIA [MEDIA ...]

Create a full-capacity ISO9660 image with one random-data file. MEDIA is one
or more of: cd-r, dvd-r, bd-r, all.

Options:
  -o, --output DIR  Output directory (default: ./images)
  -b, --blocks N    Override capacity in 2048-byte blocks (one MEDIA only)
  -f, --force       Replace an existing image of the same name
  -h, --help        Show this help

Examples:
  ./generate-media-isos.sh cd-r dvd-r
  ./generate-media-isos.sh --output /mnt/staging bd-r
  ./generate-media-isos.sh --blocks 12200000 bd-r

Roughly twice the final image size must be free while building: one temporary
random payload and the ISO output. SHA256SUMS and an image-specific .sha256
sidecar are written in the output directory.
EOF
}

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"; }
human_size() { awk -v b="$1" 'BEGIN { split("B KiB MiB GiB TiB",u," "); i=1; while(b>=1024&&i<5){b/=1024;i++}; printf "%.2f %s",b,u[i] }'; }
free_bytes() { df -Pk "$1" | awk 'NR == 2 { print $4 * 1024 }'; }

random_file() {
  local path=$1 bytes=$2 whole remainder
  whole=$((bytes / RANDOM_CHUNK_BYTES)); remainder=$((bytes % RANDOM_CHUNK_BYTES))
  : >"$path"
  (( whole == 0 )) || dd if=/dev/urandom of="$path" bs="$RANDOM_CHUNK_BYTES" count="$whole" iflag=fullblock status=progress
  (( remainder == 0 )) || dd if=/dev/urandom of="$path" bs="$remainder" count=1 iflag=fullblock oflag=append conv=notrunc status=none
}

write_manifest_entry() {
  local image=$1 entry temp manifest="$out_dir/SHA256SUMS"
  entry=$(cd "$out_dir" && sha256sum "$(basename "$image")")
  temp=$(mktemp "$out_dir/.SHA256SUMS.XXXXXX")
  [[ ! -f "$manifest" ]] || awk -v name="$(basename "$image")" '$2 != name' "$manifest" >"$temp"
  printf '%s\n' "$entry" >>"$temp"
  mv -f "$temp" "$manifest"
  printf '%s\n' "$entry" >"${image}.sha256"
}

build_image() (
  local media target_blocks image
  local staging base_image payload base_blocks payload_blocks target_bytes required available label
  media=$1
  target_blocks=$2
  image="$out_dir/optical-stress-${media}.iso"
  target_bytes=$((target_blocks * SECTOR_BYTES))
  required=$((target_bytes * 2 + 1024 * 1024 * 1024)); available=$(free_bytes "$out_dir")
  (( available >= required )) || die "need $(human_size "$required") free in $out_dir for $media; only $(human_size "$available") available"
  [[ ! -e "$image" || $force == true ]] || die "$image already exists (use --force to replace it)"
  [[ ! -e "$image" ]] || rm -f -- "$image" "${image}.sha256"

  staging=$(mktemp -d "$out_dir/.optical-stress-${media}.XXXXXX")
  base_image=$(mktemp "$out_dir/.optical-base-${media}.XXXXXX.iso")
  payload="$staging/random-payload.bin"
  trap 'rm -rf -- "$staging"; rm -f -- "$base_image"' EXIT
  label="OPTST_${media^^}"; label=${label//-/_}

  # The empty ISO determines metadata allocation. A block-aligned payload
  # then makes the finished image exactly target_blocks sectors long.
  xorriso -as mkisofs -iso-level 3 -V "$label" -o "$base_image" "$staging" >/dev/null
  base_blocks=$(( $(stat -c %s "$base_image") / SECTOR_BYTES ))
  (( base_blocks < target_blocks )) || die "$media target is smaller than ISO metadata"
  payload_blocks=$((target_blocks - base_blocks))
  printf 'Building %s: %s (%s blocks); random payload: %s\n' "$image" "$(human_size "$target_bytes")" "$target_blocks" "$(human_size "$((payload_blocks * SECTOR_BYTES))")"
  random_file "$payload" "$((payload_blocks * SECTOR_BYTES))"
  xorriso -as mkisofs -iso-level 3 -V "$label" -o "$image" "$staging" >/dev/null
  [[ $(stat -c %s "$image") -eq "$target_bytes" ]] || die "xorriso produced an unexpected image size"
  write_manifest_entry "$image"
  printf 'Created %s\n' "$image"
)

out_dir=./images; force=false; blocks_override=""; declare -a media=()
while (( $# )); do
  case "$1" in
    -o|--output) (( $# >= 2 )) || die "$1 needs a directory"; out_dir=$2; shift 2 ;;
    -b|--blocks) (( $# >= 2 )) || die "$1 needs a number"; blocks_override=$2; shift 2 ;;
    -f|--force) force=true; shift ;;
    -h|--help) usage; exit 0 ;;
    --) shift; media+=("$@"); break ;;
    -*) die "unknown option: $1" ;;
    *) media+=("$1"); shift ;;
  esac
done
(( ${#media[@]} )) || { usage >&2; exit 2; }
[[ -z $blocks_override || $blocks_override =~ ^[1-9][0-9]*$ ]] || die "--blocks must be a positive integer"
if [[ ${media[*]} == *all* ]]; then (( ${#media[@]} == 1 )) || die "all cannot be combined with another MEDIA"; media=(cd-r dvd-r bd-r); fi
[[ -z $blocks_override || ${#media[@]} -eq 1 ]] || die "--blocks can be used with one MEDIA only"
need xorriso; need sha256sum; need dd; need stat
mkdir -p -- "$out_dir"; out_dir=$(cd "$out_dir" && pwd)
declare -A seen=()
for item in "${media[@]}"; do
  [[ -n ${MEDIA_BLOCKS[$item]+yes} ]] || die "unknown MEDIA: $item"
  [[ -z ${seen[$item]+yes} ]] || die "MEDIA specified more than once: $item"
  seen[$item]=yes
  target_blocks=${MEDIA_BLOCKS[$item]}
  [[ -z $blocks_override ]] || target_blocks=$blocks_override
  build_image "$item" "$target_blocks"
done
printf 'Checksums written to %s/SHA256SUMS\n' "$out_dir"
