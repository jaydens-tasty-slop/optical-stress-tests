// sector-map compares every 2,048-byte sector on an optical device with an
// ISO image and renders the result in an Archimedean-spiral PNG.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sectorSize             = int64(2048)
	defaultSectorsPerChunk = int64(4096)  // 8 MiB: sequentially efficient.
	maxSectorsPerChunk     = int64(65536) // Bound allocations to 128 MiB per buffer.
	defaultSlowReadWarning = 5 * time.Second
)

type report struct {
	StartedUTC         string  `json:"started_utc"`
	FinishedUTC        string  `json:"finished_utc"`
	Device             string  `json:"device"`
	Image              string  `json:"image"`
	Medium             string  `json:"medium"`
	Sectors            int64   `json:"sectors"`
	StartSector        int64   `json:"start_sector"`
	SectorsPerChunk    int64   `json:"sectors_per_chunk"`
	GoodSectors        int64   `json:"good_sectors"`
	BadSectors         int64   `json:"bad_sectors"`
	ChunkReadErrors    int64   `json:"chunk_read_errors"`
	RecoveryReadCalls  int64   `json:"recovery_read_calls"`
	RecoveryReadErrors int64   `json:"recovery_read_errors"`
	SectorReadErrors   int64   `json:"sector_read_errors"`
	DeviceReadCalls    int64   `json:"device_read_calls"`
	SlowDeviceReads    int64   `json:"slow_device_reads"`
	LongestReadSeconds float64 `json:"longest_device_read_seconds"`
	ReadSeconds        float64 `json:"read_seconds"`
	Seconds            float64 `json:"elapsed_seconds"`
	MiBPerSecond       float64 `json:"average_read_mib_per_second"`
	PNG                string  `json:"png"`
	Width              int     `json:"width"`
	Height             int     `json:"height"`
	SectorsPerPixel    float64 `json:"average_sectors_per_rendered_pixel"`
}

var sectorPalette = color.Palette{
	color.RGBA{14, 16, 20, 255},    // near-black background
	color.RGBA{174, 232, 190, 255}, // pastel green: match
	color.RGBA{245, 159, 159, 255}, // pastel red: unreadable or mismatch
	color.RGBA{239, 235, 224, 255}, // off-white: embedded label text
}

// Five-by-seven glyphs keep the PNG self-contained without external fonts.
var glyphs = map[rune][7]uint8{
	' ': {0, 0, 0, 0, 0, 0, 0},
	'%': {17, 2, 4, 8, 16, 0, 0},
	'-': {0, 0, 0, 31, 0, 0, 0},
	'.': {0, 0, 0, 0, 0, 12, 12},
	':': {0, 12, 12, 0, 12, 12, 0},
	'0': {14, 17, 19, 21, 25, 17, 14},
	'1': {4, 12, 4, 4, 4, 4, 14},
	'2': {14, 17, 1, 2, 4, 8, 31},
	'3': {30, 1, 1, 14, 1, 1, 30},
	'4': {2, 6, 10, 18, 31, 2, 2},
	'5': {31, 16, 16, 30, 1, 1, 30},
	'6': {14, 16, 16, 30, 17, 17, 14},
	'7': {31, 1, 2, 4, 8, 8, 8},
	'8': {14, 17, 17, 14, 17, 17, 14},
	'9': {14, 17, 17, 15, 1, 1, 14},
	'A': {14, 17, 17, 31, 17, 17, 17},
	'B': {30, 17, 17, 30, 17, 17, 30},
	'C': {14, 17, 16, 16, 16, 17, 14},
	'D': {30, 17, 17, 17, 17, 17, 30},
	'E': {31, 16, 16, 30, 16, 16, 31},
	'F': {31, 16, 16, 30, 16, 16, 16},
	'G': {14, 17, 16, 23, 17, 17, 15},
	'H': {17, 17, 17, 31, 17, 17, 17},
	'I': {14, 4, 4, 4, 4, 4, 14},
	'J': {7, 2, 2, 2, 2, 18, 12},
	'K': {17, 18, 20, 24, 20, 18, 17},
	'L': {16, 16, 16, 16, 16, 16, 31},
	'M': {17, 27, 21, 21, 17, 17, 17},
	'N': {17, 25, 21, 19, 17, 17, 17},
	'O': {14, 17, 17, 17, 17, 17, 14},
	'P': {30, 17, 17, 30, 16, 16, 16},
	'Q': {14, 17, 17, 17, 21, 18, 13},
	'R': {30, 17, 17, 30, 20, 18, 17},
	'S': {15, 16, 16, 14, 1, 1, 30},
	'T': {31, 4, 4, 4, 4, 4, 4},
	'U': {17, 17, 17, 17, 17, 17, 14},
	'V': {17, 17, 17, 17, 17, 10, 4},
	'W': {17, 17, 17, 21, 21, 21, 10},
	'X': {17, 17, 10, 4, 10, 17, 17},
	'Y': {17, 17, 10, 4, 4, 4, 4},
	'Z': {31, 1, 2, 4, 8, 16, 31},
}

func drawText(img *image.Paletted, x, y, scale int, text string) {
	for _, char := range strings.ToUpper(text) {
		glyph, ok := glyphs[char]
		if !ok {
			glyph = glyphs[' ']
		}
		for row, bits := range glyph {
			for column := 0; column < 5; column++ {
				if bits&(1<<uint(4-column)) == 0 {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						px, py := x+column*scale+dx, y+row*scale+dy
						if image.Pt(px, py).In(img.Rect) {
							img.Pix[py*img.Stride+px] = 3
						}
					}
				}
			}
		}
		x += 6 * scale
	}
}

func drawMetadata(img *image.Paletted, r report, date string) {
	const scale, padding = 2, 10
	goodPct, badPct := 0.0, 0.0
	if r.Sectors > 0 {
		goodPct = 100 * float64(r.GoodSectors) / float64(r.Sectors)
		badPct = 100 * float64(r.BadSectors) / float64(r.Sectors)
	}
	lines := []string{
		"TYPE: " + r.Medium,
		fmt.Sprintf("GOOD: %d %.2f%%", r.GoodSectors, goodPct),
		fmt.Sprintf("BAD:  %d %.2f%%", r.BadSectors, badPct),
		"DATE: " + date,
	}
	maxChars := 0
	for _, line := range lines {
		if len(line) > maxChars {
			maxChars = len(line)
		}
	}
	width := maxChars*6*scale + padding*2
	height := len(lines)*8*scale + padding*2
	for y := padding / 2; y < height; y++ {
		for x := padding / 2; x < width; x++ {
			if image.Pt(x, y).In(img.Rect) {
				img.Pix[y*img.Stride+x] = 0
			}
		}
	}
	for i, line := range lines {
		drawText(img, padding, padding+i*8*scale, scale, line)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(2)
}

func inferMedium(imagePath string) string {
	switch filepath.Base(imagePath) {
	case "optical-stress-cd-r.iso":
		return "cd-r"
	case "optical-stress-dvd-r.iso":
		return "dvd-r"
	case "optical-stress-bd-r.iso":
		return "bd-r"
	default:
		return "unknown"
	}
}

func validMedium(s string) bool {
	return s != "" && !strings.ContainsAny(s, "/\\")
}

type scanConfig struct {
	startSector     int64
	sectorsPerChunk int64
	slowReadWarning time.Duration
}

// readAtWithDiagnostics cannot cancel a read blocked inside the kernel, but it
// keeps reporting exactly which request is outstanding while the drive or the
// source storage is retrying it.
func readAtWithDiagnostics(reader io.ReaderAt, target, phase string, buf []byte, base, count int64, warningEvery time.Duration) (int, error, time.Duration) {
	offset := base * sectorSize
	started := time.Now()
	var stop, stopped chan struct{}
	if warningEvery > 0 {
		stop = make(chan struct{})
		stopped = make(chan struct{})
		go func() {
			defer close(stopped)
			ticker := time.NewTicker(warningEvery)
			defer ticker.Stop()
			for {
				select {
				case now := <-ticker.C:
					extra := "source storage may be stalled"
					if target == "device" {
						extra = "optical drive or kernel may be retrying"
					}
					fmt.Fprintf(os.Stderr, "\nSlow %s read: phase=%s LBA=%d-%d offset=%d bytes=%d elapsed=%s; %s\n",
						target, phase, base, base+count-1, offset, len(buf), now.Sub(started).Round(100*time.Millisecond), extra)
				case <-stop:
					return
				}
			}
		}()
	}

	read, err := reader.ReadAt(buf, offset)
	elapsed := time.Since(started)
	if stop != nil {
		close(stop)
		<-stopped
	}
	if warningEvery > 0 && elapsed >= warningEvery {
		fmt.Fprintf(os.Stderr, "%s read returned: phase=%s LBA=%d-%d read=%d/%d elapsed=%s error=%v\n",
			target, phase, base, base+count-1, read, len(buf), elapsed.Round(100*time.Millisecond), err)
	}
	return read, err, elapsed
}

func recordDeviceRead(r *report, elapsed time.Duration, recovery bool, warningAfter time.Duration) {
	r.DeviceReadCalls++
	if recovery {
		r.RecoveryReadCalls++
	}
	if seconds := elapsed.Seconds(); seconds > r.LongestReadSeconds {
		r.LongestReadSeconds = seconds
	}
	if warningAfter > 0 && elapsed >= warningAfter {
		r.SlowDeviceReads++
	}
}

func compareRange(want, got []byte, base, count int64, states []byte, r *report) {
	for i := int64(0); i < count; i++ {
		from := i * sectorSize
		if bytes.Equal(want[from:from+sectorSize], got[from:from+sectorSize]) {
			states[base+i] = 1
			r.GoodSectors++
		} else {
			states[base+i] = 2
			r.BadSectors++
		}
	}
}

// recoverRange bisects a failed chunk until readable subranges or individual
// unreadable sectors are found. A single bad sector therefore does not force a
// separate read of every other sector in the original chunk.
func recoverRange(device io.ReaderAt, want, got []byte, base, count int64, states []byte, r *report, cfg scanConfig) {
	read, err, elapsed := readAtWithDiagnostics(device, "device", "recovery", got, base, count, cfg.slowReadWarning)
	recordDeviceRead(r, elapsed, true, cfg.slowReadWarning)
	if err == nil && int64(read) == count*sectorSize {
		compareRange(want, got, base, count, states, r)
		return
	}

	r.RecoveryReadErrors++
	if count == 1 {
		states[base] = 2
		r.BadSectors++
		r.SectorReadErrors++
		if r.SectorReadErrors <= 10 || r.SectorReadErrors%100 == 0 {
			fmt.Fprintf(os.Stderr, "Unreadable sector: LBA=%d read=%d/%d elapsed=%s error=%v (unreadable count=%d)\n",
				base, read, sectorSize, elapsed.Round(100*time.Millisecond), err, r.SectorReadErrors)
		}
		return
	}

	leftCount := count / 2
	leftBytes := leftCount * sectorSize
	recoverRange(device, want[:leftBytes], got[:leftBytes], base, leftCount, states, r, cfg)
	recoverRange(device, want[leftBytes:], got[leftBytes:], base+leftCount, count-leftCount, states, r, cfg)
}

func progress(processed, sectors, lastBase, lastCount int64, started time.Time, r *report) {
	elapsed := time.Since(started)
	rate := float64(processed*sectorSize) / elapsed.Seconds() / (1024 * 1024)
	eta := time.Duration(0)
	if processed > 0 && processed < sectors {
		eta = time.Duration(float64(elapsed) * float64(sectors-processed) / float64(processed))
	}
	fmt.Fprintf(os.Stderr, "\rCompared %d / %d sectors (%.1f%%); last LBA %d-%d; good=%d bad=%d; %.2f MiB/s; elapsed=%s ETA=%s    ",
		processed, sectors, 100*float64(processed)/float64(sectors), lastBase, lastBase+lastCount-1,
		r.GoodSectors, r.BadSectors, rate, elapsed.Round(time.Second), eta.Round(time.Second))
}

// scan uses buffered block reads for speed. It can begin at any LBA and wraps
// at the end of the image, while states always remain indexed by physical LBA.
func scan(device, iso io.ReaderAt, sectors int64, states []byte, r *report, cfg scanConfig) error {
	want := make([]byte, cfg.sectorsPerChunk*sectorSize)
	got := make([]byte, cfg.sectorsPerChunk*sectorSize)
	lastProgress := time.Now()
	scanStarted := time.Now()
	base := cfg.startSector

	fmt.Fprintf(os.Stderr, "Scan plan: start LBA=%d, forward with wrap, chunk=%d sectors (%d bytes), slow-read warning=%s\n",
		cfg.startSector, cfg.sectorsPerChunk, cfg.sectorsPerChunk*sectorSize, cfg.slowReadWarning)
	for processed := int64(0); processed < sectors; {
		count := cfg.sectorsPerChunk
		if remaining := sectors - processed; remaining < count {
			count = remaining
		}
		if beforeWrap := sectors - base; beforeWrap < count {
			count = beforeWrap
		}
		n := count * sectorSize
		if processed == 0 {
			fmt.Fprintf(os.Stderr, "Loading first source chunk: LBA=%d-%d offset=%d bytes=%d\n", base, base+count-1, base*sectorSize, n)
		}
		read, err, _ := readAtWithDiagnostics(iso, "source ISO", "chunk", want[:n], base, count, cfg.slowReadWarning)
		if err != nil {
			return fmt.Errorf("cannot read source ISO at LBA %d-%d: read %d/%d: %w", base, base+count-1, read, n, err)
		}
		if int64(read) != n {
			return fmt.Errorf("cannot read source ISO at LBA %d-%d: short read %d/%d", base, base+count-1, read, n)
		}

		if processed == 0 {
			fmt.Fprintf(os.Stderr, "Reading first device chunk: LBA=%d-%d offset=%d bytes=%d\n", base, base+count-1, base*sectorSize, n)
		}
		read, err, elapsed := readAtWithDiagnostics(device, "device", "chunk", got[:n], base, count, cfg.slowReadWarning)
		recordDeviceRead(r, elapsed, false, cfg.slowReadWarning)
		if err == nil && int64(read) == n {
			compareRange(want[:n], got[:n], base, count, states, r)
		} else {
			r.ChunkReadErrors++
			fmt.Fprintf(os.Stderr, "Chunk read failed: LBA=%d-%d read=%d/%d elapsed=%s error=%v; bisecting range\n",
				base, base+count-1, read, n, elapsed.Round(100*time.Millisecond), err)
			if count == 1 {
				recoverRange(device, want[:n], got[:n], base, count, states, r, cfg)
			} else {
				leftCount := count / 2
				leftBytes := leftCount * sectorSize
				recoverRange(device, want[:leftBytes], got[:leftBytes], base, leftCount, states, r, cfg)
				recoverRange(device, want[leftBytes:n], got[leftBytes:n], base+leftCount, count-leftCount, states, r, cfg)
			}
		}

		processed += count
		completedBase, completedCount := base, count
		base += count
		if base == sectors {
			base = 0
		}
		if time.Since(lastProgress) >= time.Second || processed == sectors {
			progress(processed, sectors, completedBase, completedCount, scanStarted, r)
			lastProgress = time.Now()
		}
	}
	fmt.Fprintln(os.Stderr)
	return nil
}

// render maps sector n to r = pitch*sqrt(n/pi), theta = 2*pi*r/pitch.
// That is an Archimedean spiral with approximately pitch-by-pitch pixels per
// sector. We retry with a slightly wider pitch if raster rounding collides.
func render(states []byte, maxDimension int) (*image.Paletted, float64, error) {
	const firstPitch = 2.0 // Four pixels of area per sector; avoids collisions.
	for pitch := firstPitch; pitch < 32; pitch *= 1.125 {
		radius := int(math.Ceil(pitch*math.Sqrt(float64(len(states))/math.Pi))) + 2
		side := radius*2 + 1
		if maxDimension > 0 && side > maxDimension {
			// Keep the same spiral coordinates, but bin them into the requested
			// raster. A bad sector wins over good sectors in its output pixel.
			img := image.NewPaletted(image.Rect(0, 0, maxDimension, maxDimension), sectorPalette)
			scale := float64(maxDimension-1) / float64(side-1)
			for i, state := range states {
				r := pitch * math.Sqrt((float64(i)+0.5)/math.Pi)
				theta := 2 * math.Pi * r / pitch
				x := radius + int(math.Round(r*math.Cos(theta)))
				y := radius + int(math.Round(r*math.Sin(theta)))
				sx := int(math.Round(float64(x) * scale))
				sy := int(math.Round(float64(y) * scale))
				idx := sy*img.Stride + sx
				if state == 2 || img.Pix[idx] == 0 {
					img.Pix[idx] = state
				}
			}
			return img, float64(len(states)) / float64(maxDimension*maxDimension), nil
		}
		if int64(side)*int64(side) > int64(^uint(0)>>1) {
			return nil, 0, errors.New("render dimensions exceed this system's address space")
		}
		img := image.NewPaletted(image.Rect(0, 0, side, side), sectorPalette)
		collision := false
		for i, state := range states {
			r := pitch * math.Sqrt((float64(i)+0.5)/math.Pi)
			theta := 2 * math.Pi * r / pitch
			x := radius + int(math.Round(r*math.Cos(theta)))
			y := radius + int(math.Round(r*math.Sin(theta)))
			idx := y*img.Stride + x
			if img.Pix[idx] != 0 {
				collision = true
				break
			}
			img.Pix[idx] = state
		}
		if !collision {
			return img, float64(len(states)) / float64(side*side), nil
		}
	}
	return nil, 0, errors.New("could not create a collision-free spiral map")
}

func main() {
	devicePath := flag.String("device", "", "optical block device, e.g. /dev/sr0")
	imagePath := flag.String("image", "", "source ISO used to burn the medium")
	medium := flag.String("medium", "", "log/image grouping name; inferred from image name if omitted")
	output := flag.String("output", "", "base output directory (default: beside source ISO)")
	maxDimension := flag.Int("max-dimension", 0, "maximum PNG width/height; 0 keeps one pixel per sector")
	startSector := flag.Int64("start-sector", 0, "first LBA to scan; wraps to LBA 0 after the end")
	sectorsPerChunk := flag.Int64("chunk-sectors", defaultSectorsPerChunk, "sectors per normal device read")
	slowReadWarning := flag.Duration("slow-read-warning", defaultSlowReadWarning, "repeat diagnostics while a read is blocked; 0 disables")
	flag.Parse()
	if *devicePath == "" || *imagePath == "" {
		fail("--device and --image are required")
	}
	if *medium == "" {
		*medium = inferMedium(*imagePath)
	}
	if !validMedium(*medium) {
		fail("--medium cannot be empty or contain a path separator")
	}
	if *maxDimension != 0 && *maxDimension < 64 {
		fail("--max-dimension must be 0 or at least 64")
	}
	if *sectorsPerChunk < 1 || *sectorsPerChunk > maxSectorsPerChunk {
		fail("--chunk-sectors must be between 1 and %d", maxSectorsPerChunk)
	}
	if *slowReadWarning < 0 {
		fail("--slow-read-warning cannot be negative")
	}

	info, err := os.Stat(*imagePath)
	if err != nil {
		fail("cannot stat ISO: %v", err)
	}
	if info.Size() == 0 || info.Size()%sectorSize != 0 {
		fail("ISO must have a non-zero size divisible by %d", sectorSize)
	}
	sectors := info.Size() / sectorSize
	if *startSector < 0 || *startSector >= sectors {
		fail("--start-sector must be between 0 and %d", sectors-1)
	}
	if deviceInfo, err := os.Stat(*devicePath); err != nil {
		fail("cannot stat device: %v", err)
	} else if deviceInfo.Mode()&os.ModeDevice == 0 {
		fail("%s is not a device", *devicePath)
	}
	iso, err := os.Open(*imagePath)
	if err != nil {
		fail("cannot open ISO: %v", err)
	}
	defer iso.Close()
	device, err := os.Open(*devicePath)
	if err != nil {
		fail("cannot open device: %v", err)
	}
	defer device.Close()

	baseOutput := *output
	if baseOutput == "" {
		baseOutput = filepath.Dir(*imagePath)
	}
	outputDir := filepath.Join(baseOutput, "sector-maps", *medium)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		fail("cannot create output directory: %v", err)
	}
	timestamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	started := time.Now()
	r := report{
		StartedUTC:      started.UTC().Format(time.RFC3339Nano),
		Device:          *devicePath,
		Image:           *imagePath,
		Medium:          *medium,
		Sectors:         sectors,
		StartSector:     *startSector,
		SectorsPerChunk: *sectorsPerChunk,
	}
	states := make([]byte, r.Sectors)
	fmt.Printf("Comparing %d sectors from %s against %s\n", r.Sectors, *devicePath, *imagePath)
	readStarted := time.Now()
	if err := scan(device, iso, r.Sectors, states, &r, scanConfig{
		startSector:     *startSector,
		sectorsPerChunk: *sectorsPerChunk,
		slowReadWarning: *slowReadWarning,
	}); err != nil {
		fail("scan failed: %v", err)
	}
	r.ReadSeconds = time.Since(readStarted).Seconds()

	fmt.Println("Rendering spiral sector map...")
	img, sectorsPerPixel, err := render(states, *maxDimension)
	if err != nil {
		fail("cannot render map: %v", err)
	}
	pngPath := filepath.Join(outputDir, timestamp+".png")
	pngFile, err := os.Create(pngPath)
	if err != nil {
		fail("cannot create PNG: %v", err)
	}
	if err := png.Encode(pngFile, img); err != nil {
		pngFile.Close()
		fail("cannot write PNG: %v", err)
	}
	if err := pngFile.Close(); err != nil {
		fail("cannot close PNG: %v", err)
	}
	r.FinishedUTC = time.Now().UTC().Format(time.RFC3339Nano)
	r.Seconds = time.Since(started).Seconds()
	if r.ReadSeconds > 0 {
		r.MiBPerSecond = float64(r.Sectors*sectorSize) / r.ReadSeconds / (1024 * 1024)
	}
	r.PNG = pngPath
	r.Width, r.Height = img.Bounds().Dx(), img.Bounds().Dy()
	r.SectorsPerPixel = sectorsPerPixel
	drawMetadata(img, r, started.UTC().Format("2006-01-02 15:04:05Z"))
	reportPath := filepath.Join(outputDir, timestamp+".json")
	reportFile, err := os.Create(reportPath)
	if err != nil {
		fail("cannot create JSON report: %v", err)
	}
	encoder := json.NewEncoder(reportFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(r); err != nil {
		reportFile.Close()
		fail("cannot write JSON report: %v", err)
	}
	if err := reportFile.Close(); err != nil && !errors.Is(err, io.EOF) {
		fail("cannot close JSON report: %v", err)
	}
	fmt.Printf("Done: %d good, %d bad sectors\nRead: %.1fs (%.2f MiB/s); total runtime: %.1fs\nRead calls: %d total, %d recovery; slow: %d; longest: %.1fs\nRead errors: %d chunk, %d recovery, %d sector\nMap: %dx%d (%.2f sectors/pixel)\nPNG: %s\nReport: %s\n",
		r.GoodSectors, r.BadSectors, r.ReadSeconds, r.MiBPerSecond, r.Seconds,
		r.DeviceReadCalls, r.RecoveryReadCalls, r.SlowDeviceReads, r.LongestReadSeconds,
		r.ChunkReadErrors, r.RecoveryReadErrors, r.SectorReadErrors,
		r.Width, r.Height, r.SectorsPerPixel, pngPath, reportPath)
}
