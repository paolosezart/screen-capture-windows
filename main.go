package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

const (
	smXVirtualScreen = 76
	smYVirtualScreen = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
	srccopy = 0x00CC0020
	captureBLT = 0x40000000
	dibRGBColors = 0
	biRGB = 0
	version = "0.3.0"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32 = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	procSetProcessDPIAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procGetDC = user32.NewProc("GetDC")
	procReleaseDC = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC = gdi32.NewProc("DeleteDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procDeleteObject = gdi32.NewProc("DeleteObject")
	procSelectObject = gdi32.NewProc("SelectObject")
	procBitBlt = gdi32.NewProc("BitBlt")
	procGetDIBits = gdi32.NewProc("GetDIBits")
	procGetLastError = kernel32.NewProc("GetLastError")
)

type bitmapInfoHeader struct {
	Size uint32
	Width int32
	Height int32
	Planes uint16
	BitCount uint16
	Compression uint32
	SizeImage uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed uint32
	ClrImportant uint32
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

	x := flag.Int("x", 0, "X-координата области")
	y := flag.Int("y", 0, "Y-координата области")
	width := flag.Int("width", 800, "ширина области в пикселях")
	height := flag.Int("height", 600, "высота области в пикселях")
	output := flag.String("output", "screenshot.png", "путь для сохранения PNG")
	flag.StringVar(output, "o", "screenshot.png", "сокращение для --output")
	overwrite := flag.Bool("overwrite", false, "разрешить перезапись существующего файла")
	sanitizeChar := flag.String("sanitize-char", "[!]", "замена недопустимых символов в динамических частях имени")
	showVersion := flag.Bool("version", false, "показать версию")
	activeWindow := flag.Bool("active-window", false, "захватить активное окно вместо прямоугольной области")
	delay := flag.Int("delay", 0, "задержка перед захватом в секундах (0–3600)")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "ScreenShotCli — снимок прямоугольной области рабочего стола Windows")
		fmt.Fprintln(flag.CommandLine.Output(), "")
		fmt.Fprintln(flag.CommandLine.Output(), "Использование:")
		fmt.Fprintln(flag.CommandLine.Output(), "  ScreenShotCli.exe --x 0 --y 0 --width 800 --height 600 --output \"C:\\Screenshots\\part.png\"")
		fmt.Fprintln(flag.CommandLine.Output(), "")
		fmt.Fprintln(flag.CommandLine.Output(), "Основные параметры:")
		flag.PrintDefaults()
		fmt.Fprintln(flag.CommandLine.Output(), "")
		fmt.Fprintln(flag.CommandLine.Output(), "Шаблоны имени: [YYYY] [MM] [dd] [HH] [mm] [ss] [MS] [timestamp].")
		_, _ = io.WriteString(flag.CommandLine.Output(), "Переменные окружения: %TEMP%, %USERPROFILE% и другие существующие переменные.\n")
		fmt.Fprintln(flag.CommandLine.Output(), "Если имя уже занято, добавляется -conflict01, -conflict02 и т.д.")
		fmt.Fprintln(flag.CommandLine.Output(), "Папки назначения автоматически не создаются.")
		fmt.Fprintln(flag.CommandLine.Output(), "Для --active-window можно использовать --delay 5, чтобы успеть переключиться на нужное окно.")
		fmt.Fprintln(flag.CommandLine.Output(), "Захват окна не активирует и не восстанавливает его; используется PrintWindow с экранным резервным захватом.")
	}
	flag.Parse()

	if *showVersion {
		fmt.Println("ScreenShotCli", version)
		return nil
	}
	if flag.NArg() != 0 {
		return fmt.Errorf("лишние аргументы: %v", flag.Args())
	}
	if *delay < 0 || *delay > 3600 {
		return errors.New("--delay должен быть в диапазоне от 0 до 3600 секунд")
	}
	if !*activeWindow {
		if *width <= 0 || *height <= 0 {
			return errors.New("--width и --height должны быть больше нуля")
		}
		if *width > 16384 || *height > 16384 {
			return errors.New("максимальный размер каждой стороны — 16384 пикселя")
		}
		if int64(*width)*int64(*height) > 100_000_000 {
			return errors.New("область слишком большая: максимум 100 000 000 пикселей")
		}
	}
	if *output == "" {
		return errors.New("не задан путь --output")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	enableDPIAwareness()

	if *delay > 0 {
		fmt.Printf("Захват начнётся через %d сек. Переключись на нужное окно.\n", *delay)
		time.Sleep(time.Duration(*delay) * time.Second)
	}

	var (
		img *image.RGBA
		captureX, captureY, captureWidth, captureHeight int
		outputLabel = "desktop"
		err error
	)
	if *activeWindow {
		img, captureX, captureY, captureWidth, captureHeight, outputLabel, err = captureForegroundWindow()
	} else {
		left := int32(getSystemMetric(smXVirtualScreen))
		top := int32(getSystemMetric(smYVirtualScreen))
		desktopWidth := int32(getSystemMetric(smCXVirtualScreen))
		desktopHeight := int32(getSystemMetric(smCYVirtualScreen))
		right := int64(left) + int64(desktopWidth)
		bottom := int64(top) + int64(desktopHeight)
		if int64(*x) < int64(left) || int64(*y) < int64(top) ||
			int64(*x)+int64(*width) > right || int64(*y)+int64(*height) > bottom {
			return fmt.Errorf("область выходит за границы виртуального рабочего стола (X=%d, Y=%d, ширина=%d, высота=%d)", left, top, desktopWidth, desktopHeight)
		}
		img, err = capture(*x, *y, *width, *height)
		captureX, captureY, captureWidth, captureHeight = *x, *y, *width, *height
	}
	if err != nil {
		return err
	}

	now := time.Now()
	expanded, err := expandOutputTemplate(*output, outputLabel, "ScreenShotCli.exe", *sanitizeChar, now)
	if err != nil {
		return err
	}
	requested, err := filepath.Abs(expanded)
	if err != nil {
		return fmt.Errorf("не удалось определить полный путь: %w", err)
	}

	file, actualPath, err := createOutputFile(requested, *overwrite)
	if err != nil {
		fallbackPath, fallbackErr := fallbackOutputPath(filepath.Base(requested))
		if fallbackErr != nil {
			return fmt.Errorf("не удалось создать %q: %v; резервный путь также недоступен: %w", requested, err, fallbackErr)
		}
		file, actualPath, fallbackErr = createOutputFile(fallbackPath, *overwrite)
		if fallbackErr != nil {
			return fmt.Errorf("не удалось создать %q: %w (исходная ошибка: %v)", fallbackPath, fallbackErr, err)
		}
		fmt.Fprintln(os.Stderr, "ВНИМАНИЕ: исходный путь недоступен; сохранение перенаправлено.")
		fmt.Fprintln(os.Stderr, "Запрошенный путь:", requested)
	}
	encodeErr := png.Encode(file, img)
	closeErr := file.Close()
	if encodeErr != nil {
		_ = os.Remove(actualPath)
		return fmt.Errorf("ошибка записи PNG: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("ошибка закрытия файла: %w", closeErr)
	}
	fmt.Println("Скриншот сохранён.")
	fmt.Println("Файл:", actualPath)
	fmt.Printf("Область: X=%d, Y=%d, ширина=%d, высота=%d\n", captureX, captureY, captureWidth, captureHeight)
	fmt.Println("Формат: PNG")
	return nil
}

func fallbackOutputPath(name string) (string, error) {
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if dirErr := writableDirectory(filepath.Dir(candidate)); dirErr == nil {
			return candidate, nil
		}
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", errors.New("LOCALAPPDATA не определена")
	}
	dir := filepath.Join(local, "ScreenShotCli")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func writableDirectory(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%q не является папкой", dir)
	}
	return nil
}

func enableDPIAwareness() {
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
	if screenDC == 0 { return nil, lastError("GetDC") }
	defer procReleaseDC.Call(0, screenDC)
	memoryDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memoryDC == 0 { return nil, lastError("CreateCompatibleDC") }
	defer procDeleteDC.Call(memoryDC)
	bitmap, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(width), uintptr(height))
	if bitmap == 0 { return nil, lastError("CreateCompatibleBitmap") }
	defer procDeleteObject.Call(bitmap)
	oldObject, _, _ := procSelectObject.Call(memoryDC, bitmap)
	if oldObject == 0 || oldObject == ^uintptr(0) { return nil, lastError("SelectObject") }
	ok, _, _ := procBitBlt.Call(memoryDC, 0, 0, uintptr(width), uintptr(height), screenDC, uintptr(int64(x)), uintptr(int64(y)), srccopy|captureBLT)
	if ok == 0 {
		procSelectObject.Call(memoryDC, oldObject)
		return nil, lastError("BitBlt")
	}
	procSelectObject.Call(memoryDC, oldObject)
	pixels := make([]byte, width*height*4)
	info := bitmapInfo{Header: bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: int32(width), Height: -int32(height),
		Planes: 1, BitCount: 32, Compression: biRGB, SizeImage: uint32(len(pixels)),
	}}
	lines, _, _ := procGetDIBits.Call(memoryDC, bitmap, 0, uintptr(height), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&info)), dibRGBColors)
	if lines == 0 { return nil, lastError("GetDIBits") }
	if lines != uintptr(height) { return nil, fmt.Errorf("GetDIBits вернул %d строк вместо %d", lines, height) }
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(pixels); i += 4 {
		img.Pix[i] = pixels[i+2]
		img.Pix[i+1] = pixels[i+1]
		img.Pix[i+2] = pixels[i]
		img.Pix[i+3] = 255
	}
	return img, nil
}

func lastError(operation string) error {
	code, _, _ := procGetLastError.Call()
	if code == 0 { return fmt.Errorf("%s завершился неудачно", operation) }
	return fmt.Errorf("%s: %w", operation, syscall.Errno(code))
}
