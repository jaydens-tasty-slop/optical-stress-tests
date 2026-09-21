# Optical-media stress images

`generate-media-isos.sh` creates ISO9660 images filled with random data and a local SHA-256 manifest. `verify-optical-media.sh` reads a burned disc back and checks it against that manifest.

```bash
./generate-media-isos.sh --output ./images cd-r dvd-r bd-r
# Burn images/optical-stress-bd-r.iso with your preferred burning tool.
./verify-optical-media.sh --device /dev/sr0 --image ./images/optical-stress-bd-r.iso
```

The default profiles use the writable data-image block counts for an 80-minute CD-R, single-layer DVD-R, and 25 GB-class single-layer BD-R. An 80-minute CD-R's lead-out starts at LBA 359849, and a recorder reserves a 150-sector run-out/lead-out margin, so the default CD-R image is 359699 sectors rather than the often-quoted 360000 sectors. A specific blank disc may report a smaller writable capacity; pass that 2048-byte block count to `generate-media-isos.sh --blocks N MEDIA` before burning.

Verification writes a timestamped `*.read-stats.txt` file and tees its terminal output into a timestamped `.log`, both under `verification-logs/cd-r`, `verification-logs/dvd-r`, or `verification-logs/bd-r` beside `SHA256SUMS`. It records elapsed time, hash status, Linux block-device counter deltas, `dd` diagnostics, and kernel messages where permissions allow. Optical drives usually perform retries internally and do not expose a retry count to Linux, so the stats cannot conclusively report every retry. Use `--medium TYPE` for a nonstandard image filename.

Sector mapping prints the first source and device LBA ranges immediately, then reports progress with the last completed LBA, good/bad counts, speed, and ETA. A read that remains blocked emits a diagnostic every `MAP_SLOW_READ_WARNING` (default `5s`) with its phase, exact LBA range, byte offset, size, and elapsed time. This distinguishes a slow ISO read from an optical-device read stuck in drive/kernel retries. Failed chunks are bisected until the readable ranges and individual unreadable sectors are isolated; BD mapping uses smaller 256-sector chunks by default (`BD_MAP_CHUNK_SECTORS`) so progress and failures are localized more tightly.

To probe the rest of a disc before returning to its beginning, start at another LBA; the scan proceeds forward and wraps around to sector 0:

```bash
make map-bd-r BD_MAP_START_SECTOR=4096
```

This changes scan order but does not skip sectors, and it cannot cancel a read already blocked inside the Linux kernel. Stop the current process before restarting at a different LBA. `MAP_START_SECTOR` controls the other media targets, while `MAP_SLOW_READ_WARNING=2s` increases diagnostic frequency.
