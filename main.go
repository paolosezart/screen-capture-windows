package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	smXVirtualScreen = 76
	smYVirtualScreen = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	srccopy    = 0x00CC0020
	captureBLT = 0x40000000
	dibRGBColors = 0
	biRGB = 0
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procSetProcessDPIAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC             = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC                       = gdi32.NewProc("DeleteDC")
	procCreateCompatibleBitmap         = gdi32.NewProc("CreateCompatibleBitmap")
	procDeleteObject                   = gdi32.NewProc("DeleteObject")
	procSelectObject                   = gdi32.NewProc("SelectObject")
	procBitBlt                         = gdi32.NewProc("BitBlt")
	procGetDIBits                       = gdi32.NewProc("GetDIBits")
	procGetLastError                    = kernel32.NewProc("GetLastError")
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}
}

func run() error {
	if runtime.GOOS != "windows" {
		return errors.New("эта версия программы предназначена только для Windows")
	}

	x := flag.Int("x", 0, "X-координата левого верхнего угла")
	y := flag.Int("y", 0, "Y-координата левого верхнего угла")
	width := flag.Int("width", 800, "ширина области в пикселях")
	height := flag.Int("height", 600, "высота области в пикселях")
	output := flag.String("output", "screenshot.png", "путь для сохранения PNG")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "ScreenShotCli — снимок выбранной области экрана Windows\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Использование:\n  ScreenShotCli.exe --x 0 --y 0 --width 800 --height 600 --output \"C:\\Screenshots\\part.png\"\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *width <= 0 || *height <= 0 {
		return errors.New("--width и --height должны быть больше нуля")
	}
	if *width > 16384 || *height > 16384 {
		return errors.New("максимальный размер каждой стороны — 16384 пикселя")
	}
	if int64(*width)*int64(*height) > 100_000_000 {
		return errors.New("область слишком большая: максимум 100 000 000 пикселей")
	}
	if *output == "" {
		return errors.New("не задан путь --output")
	}

	// Keep Windows API calls on one OS thread while setting process DPI awareness
	// and querying the virtual desktop geometry.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	enableDPIAwareness()

	left := int32(getSystemMetric(smXVirtualScreen))
	top := int32(getSystemMetric(smYVirtualScreen))
	desktopWidth := int32(getSystemMetric(smCXVirtualScreen))
	desktopHeight := int32(getSystemMetric(smCYVirtualScreen))
	right := int64(left) + int64(desktopWidth)
	bottom := int64(top) + int64(desktopHeight)

	if int64(*x) < int64(left) || int64(*y) < int64(top) ||
		int64(*x)+int64(*width) > right || int64(*y)+int64(*height) > bottom {
		return fmt.Errorf(
			"область выходит за границы виртуального рабочего стола (X=%d, Y=%d, ширина=%d, высота=%d)",
			left, top, desktopWidth, desktopHeight,
		)
	}

	img, err := capture(*x, *y, *width, *height)
	if err != nil {
		return err
	}

	outputPath, err := filepath.Abs(os.ExpandEnv(*output))
	if err != nil {
		return fmt.Errorf("не удалось определить полный путь: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("не удалось создать папку: %w", err)
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("не удалось создать файл %q: %w", outputPath, err)
	}
	encodeErr := png.Encode(file, img)
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("ошибка записи PNG: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("ошибка закрытия файла: %w", closeErr)
	}

	fmt.Println("Скриншот сохранён.")
	fmt.Println("Файл:", outputPath)
	fmt.Printf("Область: X=%d, Y=%d, ширина=%d, высота=%d\n", *x, *y, *width, *height)
	fmt.Println("Формат: PNG")
	return nil
}

func enableDPIAwareness() {
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4.
	if procSetProcessDPIAwarenessContext.Find() == nil {
		result, _, _ := procSetProcessDPIAwarenessContext.Call(^uintptr(3))
		if result != 0 {
			return
		}
	}
	_, _, _ = procSetProcessDPIAware.Call()
}

func getSystemMetric(index int) int {
	value, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int(int32(value))
}

func capture(x, y, width, height int) (*image.RGBA, error) {
	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return nil, lastError("GetDC")
	}
	defer procReleaseDC.Call(0, screenDC)

	memoryDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memoryDC == 0 {
		return nil, lastError("CreateCompatibleDC")
	}
	defer procDeleteDC.Call(memoryDC)

	bitmap, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return nil, lastError("CreateCompatibleBitmap")
	}
	defer procDeleteObject.Call(bitmap)

	oldObject, _, _ := procSelectObject.Call(memoryDC, bitmap)
	if oldObject == 0 || oldObject == ^uintptr(0) {
		return nil, lastError("SelectObject")
	}

	ok, _, _ := procBitBlt.Call(
		memoryDC, 0, 0, uintptr(width), uintptr(height),
		screenDC, uintptr(int64(x)), uintptr(int64(y)),
		srccopy|captureBLT,
	)
	if ok == 0 {
		procSelectObject.Call(memoryDC, oldObject)
		return nil, lastError("BitBlt")
	}

	// GetDIBits requires the bitmap not to be selected into a device context.
	procSelectObject.Call(memoryDC, oldObject)

	pixels := make([]byte, width*height*4)
	info := bitmapInfo{
		Header: bitmapInfoHeader{
			Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			Width:       int32(width),
			Height:      -int32(height), // top-down DIB
			Planes:      1,
			BitCount:    32,
			Compression: biRGB,
			SizeImage:   uint32(len(pixels)),
		},
	}

	lines, _, _ := procGetDIBits.Call(
		memoryDC, bitmap, 0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&info)),
		dibRGBColors,
	)
	if lines == 0 {
		return nil, lastError("GetDIBits")
	}
	if lines != uintptr(height) {
		return nil, fmt.Errorf("GetDIBits вернул %d строк вместо %d", lines, height)
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(pixels); i += 4 {
		img.Pix[i+0] = pixels[i+2] // BGR -> RGB
		img.Pix[i+1] = pixels[i+1]
		img.Pix[i+2] = pixels[i+0]
		img.Pix[i+3] = 255 // GDI screen captures do not provide meaningful alpha.
	}
	return img, nil
}

func lastError(operation string) error {
	code, _, _ := procGetLastError.Call()
	if code == 0 {
		return fmt.Errorf("%s завершился неудачно", operation)
	}
	return fmt.Errorf("%s: %w", operation, syscall.Errno(code))
}
