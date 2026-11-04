package main

import (
	"fmt"
	"image"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

const (
	pwRenderFullContent = 0x00000002
)

var (
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	procIsWindowVisible = user32.NewProc("IsWindowVisible")
	procGetWindowRect = user32.NewProc("GetWindowRect")
	procGetWindowTextW = user32.NewProc("GetWindowTextW")
	procPrintWindow = user32.NewProc("PrintWindow")
)

type windowRect struct {
	Left, Top, Right, Bottom int32
}

func captureForegroundWindow() (*image.RGBA, int, int, int, int, string, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return nil, 0, 0, 0, 0, "", fmt.Errorf("Windows не вернула активное окно")
	}
	console, _, _ := procGetConsoleWindow.Call()
	if console != 0 && hwnd == console {
		return nil, 0, 0, 0, 0, "", fmt.Errorf("активным оказалось окно консоли, из которой запущена программа; используй --delay 5 и за это время переключись на целевое приложение")
	}
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	if visible == 0 {
		return nil, 0, 0, 0, 0, "", fmt.Errorf("активное окно не отображается")
	}
	var rect windowRect
	ok, _, callErr := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	if ok == 0 {
		return nil, 0, 0, 0, 0, "", fmt.Errorf("GetWindowRect: %v", callErr)
	}
	width := int(rect.Right - rect.Left)
	height := int(rect.Bottom - rect.Top)
	if width <= 0 || height <= 0 {
		return nil, 0, 0, 0, 0, "", fmt.Errorf("активное окно имеет некорректный размер %dx%d", width, height)
	}
	if width > 16384 || height > 16384 || int64(width)*int64(height) > 100_000_000 {
		return nil, 0, 0, 0, 0, "", fmt.Errorf("активное окно слишком большое: %dx%d", width, height)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return nil, 0, 0, 0, 0, "", lastError("GetDC")
	}
	defer procReleaseDC.Call(0, screenDC)
	memoryDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memoryDC == 0 {
		return nil, 0, 0, 0, 0, "", lastError("CreateCompatibleDC")
	}
	defer procDeleteDC.Call(memoryDC)
	bitmap, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return nil, 0, 0, 0, 0, "", lastError("CreateCompatibleBitmap")
	}
	defer procDeleteObject.Call(bitmap)
	oldObject, _, _ := procSelectObject.Call(memoryDC, bitmap)
	if oldObject == 0 || oldObject == ^uintptr(0) {
		return nil, 0, 0, 0, 0, "", lastError("SelectObject")
	}
	printed, _, _ := procPrintWindow.Call(hwnd, memoryDC, pwRenderFullContent)
	if printed == 0 {
		// Резервный путь снимает видимую часть окна с экрана и учитывает перекрытия другими окнами.
		blitted, _, _ := procBitBlt.Call(memoryDC, 0, 0, uintptr(width), uintptr(height), screenDC,
			uintptr(int64(rect.Left)), uintptr(int64(rect.Top)), srccopy|captureBLT)
		if blitted == 0 {
			procSelectObject.Call(memoryDC, oldObject)
			return nil, 0, 0, 0, 0, "", fmt.Errorf("PrintWindow и резервный BitBlt завершились неудачно")
		}
		fmt.Fprintln(os.Stderr, "ВНИМАНИЕ: PrintWindow не поддержан окном; использован захват с экрана, перекрытые части могут содержать другие окна.")
	}
	procSelectObject.Call(memoryDC, oldObject)
	pixels := make([]byte, width*height*4)
	info := bitmapInfo{Header: bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: int32(width), Height: -int32(height),
		Planes: 1, BitCount: 32, Compression: biRGB, SizeImage: uint32(len(pixels)),
	}}
	lines, _, _ := procGetDIBits.Call(memoryDC, bitmap, 0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&info)), dibRGBColors)
	if lines == 0 || lines != uintptr(height) {
		return nil, 0, 0, 0, 0, "", lastError("GetDIBits")
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(pixels); i += 4 {
		img.Pix[i] = pixels[i+2]
		img.Pix[i+1] = pixels[i+1]
		img.Pix[i+2] = pixels[i]
		img.Pix[i+3] = 255
	}
	title := readWindowTitle(hwnd)
	if title == "" {
		title = "active-window"
	}
	return img, int(rect.Left), int(rect.Top), width, height, title, nil
}

func readWindowTitle(hwnd uintptr) string {
	buf := make([]uint16, 1024)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return strings.TrimSpace(syscall.UTF16ToString(buf[:n]))
}
