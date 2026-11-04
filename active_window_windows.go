package main

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
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
	procEnumWindows = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procOpenProcess = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle = kernel32.NewProc("CloseHandle")
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
	return captureWindow(hwnd)
}

func captureWindow(hwnd uintptr) (*image.RGBA, int, int, int, int, string, error) {
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	if visible == 0 {
		return nil, 0, 0, 0, 0, "", fmt.Errorf("активное окно не отображается")
	}
	var rect windowRect
	ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	if ok == 0 {
		return nil, 0, 0, 0, 0, "", lastError("GetWindowRect")
	}
	width := int(rect.Right - rect.Left)
	height := int(rect.Bottom - rect.Top)
	if width <= 0 || height <= 0 {
		return nil, 0, 0, 0, 0, "", fmt.Errorf("активное окно имеет некорректный размер %dx%d", width, height)
	}
	if width > 16384 || height > 16384 || int64(width)*int64(height) > 100_000_000 {
		return nil, 0, 0, 0, 0, "", fmt.Errorf("активное окно слишком большое: %dx%d", width, height)
	}

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


// findMatchingWindow returns the first visible top-level window matching a
// case-insensitive title substring or executable filename.
func findMatchingWindow(titlePart, processPart string) (uintptr, error) {
	var match uintptr
	callback := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		windowTitle := readWindowTitle(hwnd)
		if windowTitle == "" {
			return 1
		}
		if titlePart != "" {
			if strings.Contains(strings.ToLower(windowTitle), strings.ToLower(titlePart)) {
				match = hwnd
				return 0
			}
			return 1
		}
		var pid uint32
		procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid == 0 {
			return 1
		}
		process, _, _ := procOpenProcess.Call(0x1000, 0, uintptr(pid))
		if process == 0 {
			return 1
		}
		defer procCloseHandle.Call(process)
		buf := make([]uint16, 32768)
		size := uint32(len(buf))
		ok, _, _ := procQueryFullProcessImageNameW.Call(process, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
		if ok == 0 {
			return 1
		}
		name := filepath.Base(syscall.UTF16ToString(buf[:size]))
		if strings.EqualFold(name, processPart) {
			match = hwnd
			return 0
		}
		return 1
	})
	procEnumWindows.Call(callback, 0)
	if match == 0 {
		if titlePart != "" {
			return 0, fmt.Errorf("не найдено видимое окно с заголовком, содержащим %q", titlePart)
		}
		return 0, fmt.Errorf("не найдено видимое окно процесса %q", processPart)
	}
	return match, nil
}
