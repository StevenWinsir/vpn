using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Interop;
using System.Windows.Media;

namespace AsterLink.App;

/// <summary>
/// The window's system look (handoff 6.1): a dark title bar in the dark scheme (Windows 10 2004 and later), and on
/// Windows 11 22H2 and later the Mica backdrop showing through the sidebar. Every call is version-checked and a
/// failing call simply leaves the solid colours in place: nothing here can stop the window from opening.
/// </summary>
internal static class WindowBackdrop
{
    private const int DwmUseImmersiveDarkMode = 20;     // DWMWA_USE_IMMERSIVE_DARK_MODE, build 19041+
    private const int DwmWindowCornerPreference = 33;   // DWMWA_WINDOW_CORNER_PREFERENCE, Windows 11
    private const int DwmSystemBackdropType = 38;       // DWMWA_SYSTEMBACKDROP_TYPE, build 22621+
    private const int BackdropMainWindow = 2;           // DWMSBT_MAINWINDOW (Mica)
    private const int CornerRoundSmall = 3;             // DWMWCP_ROUNDSMALL
    private const uint SwpRefreshFrame = 0x0001 | 0x0002 | 0x0004 | 0x0010 | 0x0020; // NOSIZE NOMOVE NOZORDER NOACTIVATE FRAMECHANGED

    [StructLayout(LayoutKind.Sequential)]
    private struct Margins { public int Left, Right, Top, Bottom; }

    [DllImport("dwmapi.dll")]
    private static extern int DwmSetWindowAttribute(IntPtr hwnd, int attribute, ref int value, int size);

    [DllImport("dwmapi.dll")]
    private static extern int DwmExtendFrameIntoClientArea(IntPtr hwnd, ref Margins margins);

    [DllImport("user32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool SetWindowPos(IntPtr hwnd, IntPtr after, int x, int y, int cx, int cy, uint flags);

    public static bool MicaAvailable => OperatingSystem.IsWindowsVersionAtLeast(10, 0, 22621);

    /// <summary>What the window got: "mica", "solid", or "solid (…)" when a call failed.</summary>
    public static string Describe(Window window) => window.Tag as string ?? "solid";

    /// <summary>Apply (or re-apply after a theme switch) the title bar colour and, where available, Mica.</summary>
    public static void Apply(Window window, bool dark)
    {
        var handle = new WindowInteropHelper(window).Handle;
        if (handle == IntPtr.Zero) return;
        try
        {
            var flag = dark ? 1 : 0;
            DwmSetWindowAttribute(handle, DwmUseImmersiveDarkMode, ref flag, sizeof(int));
            if (MicaAvailable && TryMica(window, handle))
            {
                window.Tag = "mica";
            }
            else
            {
                window.Tag = "solid";
            }
            // Windows repaints the caption on the next frame change; without this a live theme switch keeps the old colour.
            SetWindowPos(handle, IntPtr.Zero, 0, 0, 0, 0, SwpRefreshFrame);
        }
        catch (Exception error) when (error is DllNotFoundException or EntryPointNotFoundException or COMException)
        {
            UseSolid(window, "solid (" + error.GetType().Name + ")");
        }
    }

    private static bool TryMica(Window window, IntPtr handle)
    {
        var backdrop = BackdropMainWindow;
        if (DwmSetWindowAttribute(handle, DwmSystemBackdropType, ref backdrop, sizeof(int)) != 0) return false;
        var margins = new Margins { Left = -1, Right = -1, Top = -1, Bottom = -1 };
        if (DwmExtendFrameIntoClientArea(handle, ref margins) != 0) return false;
        if (HwndSource.FromHwnd(handle)?.CompositionTarget is { } target) target.BackgroundColor = Colors.Transparent;
        // The window and the sidebar let the backdrop through; the page keeps its own background colour.
        window.Background = Brushes.Transparent;
        window.Resources["SidebarBrush"] = Brushes.Transparent;
        return true;
    }

    private static void UseSolid(Window window, string why)
    {
        window.Tag = why;
        window.Resources.Remove("SidebarBrush");
        window.SetResourceReference(Window.BackgroundProperty, "BackgroundBrush");
    }

    /// <summary>Small rounded corners for a pop-up (the tray menu) on Windows 11; ignored elsewhere.</summary>
    public static void RoundCorners(IntPtr handle)
    {
        if (handle == IntPtr.Zero || !OperatingSystem.IsWindowsVersionAtLeast(10, 0, 22000)) return;
        try
        {
            var preference = CornerRoundSmall;
            DwmSetWindowAttribute(handle, DwmWindowCornerPreference, ref preference, sizeof(int));
        }
        catch (Exception error) when (error is DllNotFoundException or EntryPointNotFoundException) { }
    }
}
