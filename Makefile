# Override these at invocation time when needed, for example:
#   make map-bd-r BD_DEVICE=/dev/sr3 MEDIA_DIR=/path/to/images

DEVICE ?=
CD_DEVICE ?= $(if $(DEVICE),$(DEVICE),/dev/sr1)
DVD_DEVICE ?= $(if $(DEVICE),$(DEVICE),/dev/sr1)
BD_DEVICE ?= $(if $(DEVICE),$(DEVICE),/dev/sr2)
MAP_MAX_DIMENSION ?= 0
BD_MAP_MAX_DIMENSION ?= 1000
MAP_CHUNK_SECTORS ?= 4096
BD_MAP_CHUNK_SECTORS ?= 256
MAP_START_SECTOR ?= 0
BD_MAP_START_SECTOR ?= $(MAP_START_SECTOR)
MAP_SLOW_READ_WARNING ?= 5s
MEDIA_DIR ?= /home/user/stress-tests
VERIFY ?= ./verify-optical-media.sh
SECTOR_MAP ?= ./bin/sector-map
GOCACHE ?= /tmp/optical-stress-go-cache
CD_IMAGE ?= $(MEDIA_DIR)/optical-stress-cd-r.iso
DVD_IMAGE ?= $(MEDIA_DIR)/optical-stress-dvd-r.iso
BD_IMAGE ?= $(MEDIA_DIR)/optical-stress-bd-r.iso

.PHONY: help test-cd-r test-cd-ram test-dvd-r test-dvd-r-questionable test-bd-r test-all \
	map-cd-r map-cd-ram map-dvd-r map-dvd-r-questionable map-bd-r

help:
	@printf '%s\n' \
	  'make test-cd-r    Verify a CD-R in CD_DEVICE (default: /dev/sr0)' \
	  'make test-cd-ram  Verify a CD-RAM disc in CD_DEVICE' \
	  'make test-dvd-r   Verify a single-layer DVD-R in DVD_DEVICE (default: /dev/sr0)' \
	  'make test-dvd-r-questionable  Verify the DVD-R that had burn issues' \
	  'make test-bd-r    Verify a BD-R in BD_DEVICE (default: /dev/sr2)' \
	  'make test-all     Run all tests sequentially (swap media when prompted)' \
	  'make map-cd-r     Create a timestamped spiral sector map for a CD-R' \
	  'make map-cd-ram   Create a timestamped spiral sector map for a CD-RAM' \
	  'make map-dvd-r    Create a timestamped spiral sector map for a DVD-R' \
	  'make map-dvd-r-questionable  Map the DVD-R that had burn issues' \
	  'make map-bd-r     Create a timestamped spiral sector map for a BD-R' \
	  '' \
	  'Override drives: make map-bd-r BD_DEVICE=/dev/sr3' \
	  'Map size: make map-bd-r BD_MAP_MAX_DIMENSION=2000' \
	  'Start elsewhere: make map-bd-r BD_MAP_START_SECTOR=4096' \
	  'Read diagnostics: make map-bd-r MAP_SLOW_READ_WARNING=2s'

test-cd-r:
	$(VERIFY) --device "$(CD_DEVICE)" --image "$(CD_IMAGE)" --medium cd-r

test-cd-ram:
	$(VERIFY) --device "$(CD_DEVICE)" --image "$(CD_IMAGE)" --medium cd-ram

test-dvd-r:
	$(VERIFY) --device "$(DVD_DEVICE)" --image "$(DVD_IMAGE)" --medium dvd-r

test-dvd-r-questionable:
	$(VERIFY) --device "$(DVD_DEVICE)" --image "$(DVD_IMAGE)" --medium dvd-r-questionable

test-bd-r:
	$(VERIFY) --device "$(BD_DEVICE)" --image "$(BD_IMAGE)" --medium bd-r

test-all:
	$(MAKE) --no-print-directory test-cd-r CD_DEVICE="$(CD_DEVICE)" MEDIA_DIR="$(MEDIA_DIR)" VERIFY="$(VERIFY)" CD_IMAGE="$(CD_IMAGE)"
	@read -r -p 'Insert the CD-RAM in $(CD_DEVICE), then press Enter to continue: ' _
	$(MAKE) --no-print-directory test-cd-ram CD_DEVICE="$(CD_DEVICE)" MEDIA_DIR="$(MEDIA_DIR)" VERIFY="$(VERIFY)" CD_IMAGE="$(CD_IMAGE)"
	@read -r -p 'Insert the DVD-R in $(DVD_DEVICE), then press Enter to continue: ' _
	$(MAKE) --no-print-directory test-dvd-r DVD_DEVICE="$(DVD_DEVICE)" MEDIA_DIR="$(MEDIA_DIR)" VERIFY="$(VERIFY)" DVD_IMAGE="$(DVD_IMAGE)"
	@read -r -p 'Insert the BD-R in $(BD_DEVICE), then press Enter to continue: ' _
	$(MAKE) --no-print-directory test-bd-r BD_DEVICE="$(BD_DEVICE)" MEDIA_DIR="$(MEDIA_DIR)" VERIFY="$(VERIFY)" BD_IMAGE="$(BD_IMAGE)"

$(SECTOR_MAP): cmd/sector-map/main.go go.mod
	mkdir -p "$(dir $@)"
	GOCACHE="$(GOCACHE)" go build -o "$@" ./cmd/sector-map

map-cd-r: $(SECTOR_MAP)
	$(SECTOR_MAP) --device "$(CD_DEVICE)" --image "$(CD_IMAGE)" --medium cd-r --max-dimension "$(MAP_MAX_DIMENSION)" --chunk-sectors "$(MAP_CHUNK_SECTORS)" --start-sector "$(MAP_START_SECTOR)" --slow-read-warning "$(MAP_SLOW_READ_WARNING)" --output "$(MEDIA_DIR)"

map-cd-ram: $(SECTOR_MAP)
	$(SECTOR_MAP) --device "$(CD_DEVICE)" --image "$(CD_IMAGE)" --medium cd-ram --max-dimension "$(MAP_MAX_DIMENSION)" --chunk-sectors "$(MAP_CHUNK_SECTORS)" --start-sector "$(MAP_START_SECTOR)" --slow-read-warning "$(MAP_SLOW_READ_WARNING)" --output "$(MEDIA_DIR)"

map-dvd-r: $(SECTOR_MAP)
	$(SECTOR_MAP) --device "$(DVD_DEVICE)" --image "$(DVD_IMAGE)" --medium dvd-r --max-dimension "$(MAP_MAX_DIMENSION)" --chunk-sectors "$(MAP_CHUNK_SECTORS)" --start-sector "$(MAP_START_SECTOR)" --slow-read-warning "$(MAP_SLOW_READ_WARNING)" --output "$(MEDIA_DIR)"

map-dvd-r-questionable: $(SECTOR_MAP)
	$(SECTOR_MAP) --device "$(DVD_DEVICE)" --image "$(DVD_IMAGE)" --medium dvd-r-questionable --max-dimension "$(MAP_MAX_DIMENSION)" --chunk-sectors "$(MAP_CHUNK_SECTORS)" --start-sector "$(MAP_START_SECTOR)" --slow-read-warning "$(MAP_SLOW_READ_WARNING)" --output "$(MEDIA_DIR)"

map-bd-r: $(SECTOR_MAP)
	$(SECTOR_MAP) --device "$(BD_DEVICE)" --image "$(BD_IMAGE)" --medium bd-r --max-dimension "$(BD_MAP_MAX_DIMENSION)" --chunk-sectors "$(BD_MAP_CHUNK_SECTORS)" --start-sector "$(BD_MAP_START_SECTOR)" --slow-read-warning "$(MAP_SLOW_READ_WARNING)" --output "$(MEDIA_DIR)"
