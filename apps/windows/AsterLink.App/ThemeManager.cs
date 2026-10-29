using System.IO;
using System.Windows;
using Microsoft.Win32;

namespace AsterLink.App;

/// <summary>Light, dark, or follow Windows. The theme is dictionary index 0 of the application resources; the styles use DynamicResource so a switch repaints live.</summary>
public static class ThemeManager
{
    /// <summary>Raised after every switch with the scheme now in use, so the title bar, Mica and the tray menu follow.</summary>
    public static event Action<bool>? Applied;

    public static bool IsDark { get; private set; }

    public static bool SystemPrefersDark()
    {
        try
        {
            using var key = Registry.CurrentUser.OpenSubKey(@"Software\Microsoft\Windows\CurrentVersion\Themes\Personalize");
            return key?.GetValue("AppsUseLightTheme") is int light && light == 0;
        }
        catch (Exception error) when (error is System.Security.SecurityException or UnauthorizedAccessException or IOException)
        {
            return false;
        }
    }

    public static void Apply(string theme)
    {
        var dark = theme == "dark" || (theme == "system" && SystemPrefersDark());
        var source = new Uri(dark ? "Theme/Dark.xaml" : "Theme/Light.xaml", UriKind.Relative);
        var merged = Application.Current.Resources.MergedDictionaries;
        var replacement = new ResourceDictionary { Source = source };
        if (merged.Count > 0) merged[0] = replacement;
        else merged.Add(replacement);
        IsDark = dark;
        Applied?.Invoke(dark);
    }
}
