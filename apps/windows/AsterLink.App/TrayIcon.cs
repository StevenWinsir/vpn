using System.Drawing;
using System.Reflection;
using System.Windows.Forms;
using Microsoft.Win32;

namespace AsterLink.App;

/// <summary>
/// The notification-area icon. Closing the window while a tunnel is up leaves AsterLink here; "退出" is the one
/// entry that stops the proxy. The three state icons are the brand mark (frontend/brand/mark.svg) in several
/// sizes each, embedded as resources; Windows picks the frame closest to the current small-icon size, and the
/// icon is loaded again when the display or its scale changes. The state is always in the tooltip text too,
/// never colour alone.
/// </summary>
public sealed class TrayIcon : IDisposable
{
    private readonly NotifyIcon icon;
    private readonly ToolStripMenuItem open = new("打开 AsterLink");
    private readonly ToolStripMenuItem toggle = new("连接");
    private readonly ToolStripMenuItem exit = new("退出（停止代理）");
    private readonly Dictionary<string, Icon> loaded = new();
    private string name = "tray-idle.ico";

    public TrayIcon(Action openWindow, Action toggleConnection, Action exitApp)
    {
        var menu = new ContextMenuStrip();
        menu.Items.Add(open);
        menu.Items.Add(toggle);
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add(exit);
        open.Click += (_, _) => openWindow();
        toggle.Click += (_, _) => toggleConnection();
        exit.Click += (_, _) => exitApp();

        icon = new NotifyIcon { Icon = Load(name), Text = "AsterLink", ContextMenuStrip = menu, Visible = true };
        icon.MouseClick += (_, e) => { if (e.Button == MouseButtons.Left) openWindow(); };
        SystemEvents.DisplaySettingsChanged += OnDisplaySettingsChanged;
    }

    public void Update(string tooltip, string state, bool signedIn, bool canToggle, bool connected)
    {
        // NotifyIcon.Text is limited to 63 characters.
        icon.Text = tooltip.Length > 63 ? tooltip[..63] : tooltip;
        name = state switch { "connected" => "tray-live.ico", "error" or "blocked" => "tray-error.ico", _ => "tray-idle.ico" };
        icon.Icon = Load(name);
        toggle.Text = connected ? "断开" : "连接";
        toggle.Enabled = signedIn && canToggle;
    }

    public void Notify(string title, string text) => icon.ShowBalloonTip(6000, title, text, ToolTipIcon.Info);

    public void Dispose()
    {
        SystemEvents.DisplaySettingsChanged -= OnDisplaySettingsChanged;
        icon.Visible = false;
        icon.Dispose();
        foreach (var item in loaded.Values) item.Dispose();
    }

    private Icon Load(string resource)
    {
        if (loaded.TryGetValue(resource, out var cached)) return cached;
        using var stream = Assembly.GetExecutingAssembly().GetManifestResourceStream(resource)
            ?? throw new InvalidOperationException("missing resource " + resource);
        var made = new Icon(stream, SystemInformation.SmallIconSize);
        loaded[resource] = made;
        return made;
    }

    // Raised on a system thread when the resolution or the scale changes: load the frame that now fits.
    private void OnDisplaySettingsChanged(object? sender, EventArgs e) =>
        System.Windows.Application.Current?.Dispatcher.BeginInvoke(new Action(() =>
        {
            var previous = loaded.Values.ToList();
            loaded.Clear();
            icon.Icon = Load(name);
            foreach (var item in previous) item.Dispose();
        }));
}
