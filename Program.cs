using System.Drawing;
using System.Drawing.Imaging;
using System.Globalization;
using System.Runtime.InteropServices;
using System.Text;
using System.Windows.Forms;

namespace ScreenShotCli;

internal static class Program
{
    private const int MaxDimension = 16_384;
    private const long MaxPixels = 100_000_000;

    private static int Main(string[] args)
    {
        Console.OutputEncoding = new UTF8Encoding(encoderShouldEmitUTF8Identifier: false);
        if (args.Length == 0 || args.Any(a => a is "--help" or "-h" or "/?"))
        {
            PrintHelp();
            return args.Length == 0 ? 2 : 0;
        }

        try
        {
            var options = ParseArguments(args);
            EnablePerMonitorDpiAwareness();
            if (options.Width <= 0 || options.Height <= 0)
                throw new ArgumentException("--width и --height должны быть больше нуля.");
            if (options.Width > MaxDimension || options.Height > MaxDimension)
                throw new ArgumentException($"Максимальный размер каждой стороны — {MaxDimension} пикселей.");
            if ((long)options.Width * options.Height > MaxPixels)
                throw new ArgumentException($"Область слишком большая: максимум {MaxPixels:N0} пикселей.");

            var virtualScreen = SystemInformation.VirtualScreen;
            long right = (long)options.X + options.Width;
            long bottom = (long)options.Y + options.Height;
            if (options.X < virtualScreen.Left || options.Y < virtualScreen.Top ||
                right > virtualScreen.Right || bottom > virtualScreen.Bottom)
                throw new ArgumentException($"Область выходит за пределы виртуального рабочего стола. Рабочий стол: X={virtualScreen.Left}, Y={virtualScreen.Top}, ширина={virtualScreen.Width}, высота={virtualScreen.Height}. Запрошено: X={options.X}, Y={options.Y}, ширина={options.Width}, высота={options.Height}.");

            string outputPath = Path.GetFullPath(Environment.ExpandEnvironmentVariables(options.OutputPath));
            string? outputDirectory = Path.GetDirectoryName(outputPath);
            if (string.IsNullOrWhiteSpace(outputDirectory))
                throw new ArgumentException("Не удалось определить папку для выходного файла.");
            Directory.CreateDirectory(outputDirectory);

            using var bitmap = new Bitmap(options.Width, options.Height, PixelFormat.Format32bppArgb);
            using (var graphics = Graphics.FromImage(bitmap))
                graphics.CopyFromScreen(options.X, options.Y, 0, 0, new Size(options.Width, options.Height), CopyPixelOperation.SourceCopy);
            bitmap.Save(outputPath, ImageFormat.Png);

            Console.WriteLine("Скриншот сохранён.");
            Console.WriteLine($"Файл: {outputPath}");
            Console.WriteLine($"Область: X={options.X}, Y={options.Y}, ширина={options.Width}, высота={options.Height}");
            Console.WriteLine("Формат: PNG");
            return 0;
        }
        catch (ArgumentException ex)
        {
            Console.Error.WriteLine($"Ошибка параметров: {ex.Message}");
            Console.Error.WriteLine("Используйте --help для справки.");
            return 2;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine($"Ошибка: {ex.Message}");
            return 1;
        }
    }

    private static Options ParseArguments(string[] args)
    {
        int? x = null, y = null, width = null, height = null;
        string? output = null;
        for (int i = 0; i < args.Length; i++)
        {
            string option = args[i];
            if (option is "-o" or "--output")
            {
                output = GetValue(args, ref i, option);
                continue;
            }
            string value = GetValue(args, ref i, option);
            int parsed = int.Parse(value, NumberStyles.Integer, CultureInfo.InvariantCulture);
            switch (option)
            {
                case "--x": x = parsed; break;
                case "--y": y = parsed; break;
                case "--width": width = parsed; break;
                case "--height": height = parsed; break;
                default: throw new ArgumentException($"Неизвестный параметр: {option}");
            }
        }
        if (x is null || y is null || width is null || height is null || string.IsNullOrWhiteSpace(output))
            throw new ArgumentException("Нужны параметры --x, --y, --width, --height и --output.");
        return new Options(x.Value, y.Value, width.Value, height.Value, output);
    }

    private static string GetValue(string[] args, ref int index, string option)
    {
        if (index + 1 >= args.Length || args[index + 1].StartsWith("--", StringComparison.Ordinal))
            throw new ArgumentException($"Для параметра {option} не задано значение.");
        index++;
        return args[index];
    }

    private static void EnablePerMonitorDpiAwareness()
    {
        try { if (SetProcessDpiAwarenessContext(new nint(-4))) return; }
        catch (EntryPointNotFoundException) { }
        try { SetProcessDPIAware(); }
        catch (EntryPointNotFoundException) { }
    }

    [DllImport("user32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool SetProcessDpiAwarenessContext(nint dpiContext);

    [DllImport("user32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool SetProcessDPIAware();

    private static void PrintHelp()
    {
        Console.WriteLine("""
        ScreenShotCli — создание скриншота области рабочего стола в PNG.
        Использование:
          ScreenShotCli.exe --x X --y Y --width ШИРИНА --height ВЫСОТА --output "ПУТЬ\ФАЙЛ.png"
        Параметры:
          --x          X-координата левого верхнего угла.
          --y          Y-координата левого верхнего угла.
          --width      Ширина в пикселях.
          --height     Высота в пикселях.
          --output     Путь выходного PNG-файла.
          -o           Короткий вариант --output.
          --help       Показать справку.
        Примеры:
          ScreenShotCli.exe --x 0 --y 0 --width 800 --height 600 --output "C:\Screenshots\part.png"
          ScreenShotCli.exe --x 100 --y 200 --width 1280 --height 720 -o "D:\Capture\screen.png"
        Координаты считаются от левого верхнего угла виртуального рабочего стола Windows.
        Координаты мониторов слева/выше основного могут быть отрицательными.
        Папка создаётся автоматически; существующий файл перезаписывается.
        """);
    }

    private sealed record Options(int X, int Y, int Width, int Height, string OutputPath);
}
