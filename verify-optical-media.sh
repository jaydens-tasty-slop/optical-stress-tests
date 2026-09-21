#!/usr/bin/env bash
# Verify a burned image against a local SHA256SUMS entry and record read stats.
set -euo pipefail

readonly SECTOR_BYTES=2048 READ_CHUNK_BYTES=$((4 * 1024 * 1024))

usage() {
  cat <<'EOF'
Usage: verify-optical-media.sh --device DEVICE --image IMAGE [options]

Read exactly IMAGE's byte length from DEVICE, compare the SHA-256 digest with
the local manifest, and save read statistics next to that manifest.

Options:
  -d, --device DEVICE  Optical block device, e.g. /dev/sr0 (required)
  -i, --image IMAGE    Locally retained source image (required; not read)
  -m, --manifest FILE  SHA256SUMS (default: IMAGE directory/SHA256SUMS)
  -M, --medium TYPE    Medium type for log grouping (default: inferred from
                        optical-stress-{cd-r,dvd-r,bd-r}.iso)
  -s, --stats FILE     Stats output (default: dated file in verification-logs/TYPE)
  -l, --log FILE       Tee terminal output to this file (default: dated file
                        in verification-logs/TYPE)
      --no-kernel-log  Do not request new kernel messages from journalctl
  -h, --help           Show this help

The device must be readable by this user. This script never writes to media.
EOF
}

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"; }
read_block_stat() { [[ -r "/sys/class/block/$1/stat" ]] && cat "/sys/class/block/$1/stat" || true; }
stat_field() { awk -v n="$2" '{print (NF >= n ? $n : 0)}' <<<"$1"; }

device=""; image=""; manifest=""; stats_file=""; log_file=""; medium_type=""; kernel_log=true
while (( $# )); do
  case "$1" in
    -d|--device) (( $# >= 2 )) || die "$1 needs a device"; device=$2; shift 2 ;;
    -i|--image) (( $# >= 2 )) || die "$1 needs an image"; image=$2; shift 2 ;;
    -m|--manifest) (( $# >= 2 )) || die "$1 needs a file"; manifest=$2; shift 2 ;;
    -M|--medium) (( $# >= 2 )) || die "$1 needs a type"; medium_type=$2; shift 2 ;;
    -s|--stats) (( $# >= 2 )) || die "$1 needs a file"; stats_file=$2; shift 2 ;;
    -l|--log) (( $# >= 2 )) || die "$1 needs a file"; log_file=$2; shift 2 ;;
    --no-kernel-log) kernel_log=false; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done
[[ -n $device && -n $image ]] || { usage >&2; exit 2; }
[[ -b $device ]] || die "$device is not a block device"
[[ -f $image ]] || die "image does not exist: $image"
image=$(cd "$(dirname "$image")" && pwd)/$(basename "$image")
manifest=${manifest:-"$(dirname "$image")/SHA256SUMS"}
[[ -f $manifest ]] || die "manifest does not exist: $manifest"
need dd; need sha256sum; need stat; need awk

image_name=$(basename "$image")
expected=$(awk -v name="$image_name" '$2 == name { hash=$1 } END { print hash }' "$manifest")
[[ $expected =~ ^[[:xdigit:]]{64}$ ]] || die "no SHA-256 entry for $image_name in $manifest"
if [[ -z $medium_type ]]; then
  case "$image_name" in
    optical-stress-cd-r.iso) medium_type=cd-r ;;
    optical-stress-dvd-r.iso) medium_type=dvd-r ;;
    optical-stress-bd-r.iso) medium_type=bd-r ;;
    *) medium_type=unknown ;;
  esac
fi
[[ $medium_type =~ ^[A-Za-z0-9._-]+$ ]] || die "--medium may contain only letters, digits, dot, underscore, and hyphen"
bytes=$(stat -c %s "$image")
(( bytes > 0 && bytes % SECTOR_BYTES == 0 )) || die "image size must be a non-zero multiple of $SECTOR_BYTES"
blocks=$((bytes / SECTOR_BYTES)); whole=$((bytes / READ_CHUNK_BYTES)); remainder=$((bytes % READ_CHUNK_BYTES)); remainder_blocks=$((remainder / SECTOR_BYTES))
device_real=$(readlink -f "$device"); device_name=$(basename "$device_real")
before=$(read_block_stat "$device_name")
start_utc=$(date --utc +%Y-%m-%dT%H:%M:%SZ); start_ns=$(date +%s%N); timestamp=$(date --utc +%Y%m%dT%H%M%S.%NZ)
log_dir="$(dirname "$manifest")/verification-logs/$medium_type"
stats_file=${stats_file:-"$log_dir/${timestamp}.read-stats.txt"}
log_file=${log_file:-"$log_dir/${timestamp}.log"}
mkdir -p -- "$(dirname "$stats_file")" "$(dirname "$log_file")"
exec > >(tee -a "$log_file") 2>&1
printf 'Verification log: %s\nMedium type: %s\n' "$log_file" "$medium_type"
hash_file=$(mktemp "$(dirname "$stats_file")/.optical-hash.XXXXXX")
dd_file=$(mktemp "$(dirname "$stats_file")/.optical-dd.XXXXXX")
kernel_file=$(mktemp "$(dirname "$stats_file")/.optical-kernel.XXXXXX")
trap 'rm -f -- "$hash_file" "$dd_file" "$kernel_file"' EXIT

printf 'Reading %s blocks (%s bytes) from %s ...\n' "$blocks" "$bytes" "$device_real"
set +e
{
  reader_status=0
  (( whole == 0 )) || dd if="$device_real" bs="$READ_CHUNK_BYTES" count="$whole" iflag=fullblock status=progress || reader_status=$?
  (( remainder == 0 )) || dd if="$device_real" bs="$SECTOR_BYTES" skip="$((whole * READ_CHUNK_BYTES / SECTOR_BYTES))" count="$remainder_blocks" iflag=fullblock status=none || reader_status=$?
  exit "$reader_status"
} 2>"$dd_file" | sha256sum >"$hash_file"
pipe_status=("${PIPESTATUS[@]}")
set -e
end_ns=$(date +%s%N); end_utc=$(date --utc +%Y-%m-%dT%H:%M:%SZ); after=$(read_block_stat "$device_name")
actual=$(awk '{print $1}' "$hash_file")
elapsed_ns=$((end_ns - start_ns))
elapsed_seconds=$(awk -v n="$elapsed_ns" 'BEGIN {printf "%.3f",n/1000000000}')
rate=$(awk -v b="$bytes" -v n="$elapsed_ns" 'BEGIN {if(n) printf "%.2f",b/(n/1000000000)/1048576; else print "0"}')

kernel_result="disabled"
if [[ $kernel_log == true ]]; then
  if command -v journalctl >/dev/null 2>&1 && journalctl -k --since "$start_utc" --until "$end_utc" --no-pager 2>/dev/null >"$kernel_file"; then kernel_result=recorded; else kernel_result="unavailable (permission or journalctl)"; fi
fi
{
  printf 'verification_started_utc=%s\nverification_finished_utc=%s\nmedium_type=%s\ndevice=%s\nimage=%s\nmanifest=%s\nlog_file=%s\n' "$start_utc" "$end_utc" "$medium_type" "$device_real" "$image" "$manifest" "$log_file"
  printf 'bytes_requested=%s\nblocks_requested=%s\nelapsed_seconds=%s\naverage_read_mib_per_second=%s\n' "$bytes" "$blocks" "$elapsed_seconds" "$rate"
  printf 'reader_exit_status=%s\nsha256sum_exit_status=%s\nexpected_sha256=%s\nactual_sha256=%s\n' "${pipe_status[0]}" "${pipe_status[1]}" "$expected" "$actual"
  if [[ ${pipe_status[0]} -eq 0 && ${pipe_status[1]} -eq 0 && $actual == "$expected" ]]; then printf 'result=PASS\n'; else printf 'result=FAIL\n'; fi
  if [[ -n $before && -n $after ]]; then
    printf 'block_stat_read_ios_delta=%s\n' "$(( $(stat_field "$after" 1) - $(stat_field "$before" 1) ))"
    printf 'block_stat_read_sectors_delta=%s\n' "$(( $(stat_field "$after" 3) - $(stat_field "$before" 3) ))"
    printf 'block_stat_read_milliseconds_delta=%s\n' "$(( $(stat_field "$after" 4) - $(stat_field "$before" 4) ))"
  else printf 'block_stat=unavailable\n'; fi
  printf 'dd_stderr_begin\n'; sed -n '1,200p' "$dd_file"; printf 'dd_stderr_end\n'
  printf 'kernel_log=%s\n' "$kernel_result"
  [[ $kernel_result != recorded ]] || { printf 'kernel_log_begin\n'; sed -n '1,500p' "$kernel_file"; printf 'kernel_log_end\n'; }
  printf '%s\n' 'note=Drive-internal retries are not exposed by Linux block-device counters. Kernel errors and resets, when readable, are recorded above; a clean hash does not prove that no internal retry occurred.'
} >"$stats_file"

if [[ ${pipe_status[0]} -eq 0 && ${pipe_status[1]} -eq 0 && $actual == "$expected" ]]; then printf 'PASS: %s\nRead statistics: %s\n' "$image_name" "$stats_file"; exit 0; fi
printf 'FAIL: expected %s, got %s (stats: %s)\n' "$expected" "$actual" "$stats_file" >&2
exit 1
