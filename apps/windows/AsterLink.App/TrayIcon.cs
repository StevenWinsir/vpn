using System.Drawing;
using System.Reflection;
using System.Windows.Forms;
using Microsoft.Win32;

namespace AsterLink.App;

/// <summary>
/// The notification-area icon. Closing the window always leaves AsterLink here (handoff 6.4); "退出" is the one entry
/// that disconnects and quits. Left click opens the window; the right-click menu starts with a status line, then
/// 打开 / 连接·断开 / 退出, drawn in the app's own colours (light or dark) instead of the classic grey menu. The three state
/// icons are the brand mark (frontend/brand/mark.svg) in several sizes each; Windows picks the frame closest to the current
/// small-icon size, and the icon is loaded again when the display or its scale changes. The state is always in the tooltip
/// and the status line too, never colour alone.
/// </summary>
public sealed class TrayIcon : IDisposable
{
    private readonly NotifyIcon icon;
    private readonly TrayMenu menu;
    private readonly Dictionary<string, Icon> loaded = new();
    private string name = "tray-idle.ico";
    private string lastUpdate = "";

    public TrayIcon(Action openWindow, Action toggleConnection, Action exitApp)
    {
        menu = new TrayMenu(openWindow, toggleConnection, exitApp);
        icon = new NotifyIcon { Icon = Load(name), Text = "AsterLink", ContextMenuStrip = menu.Strip, Visible = true };
        icon.MouseClick += (_, e) => { if (e.Button == MouseButtons.Left) openWindow(); };
        SystemEvents.DisplaySettingsChanged += OnDisplaySettingsChanged;
    }

    public void Update(string tooltip, string statusLine, string state, bool signedIn, bool canToggle, bool connected)
    {
        // Called on every view-model change: touch the shell only when something it shows actually changed.
        var key = string.Join('\u0001', tooltip, statusLine, state, signedIn, canToggle, connected);
        if (key == lastUpdate) return;
        lastUpdate = key;
        // NotifyIcon.Text is limited to 63 characters.
        icon.Text = tooltip.Length > 63 ? tooltip[..63] : tooltip;
        name = state switch { "connected" => "tray-live.ico", "error" or "blocked" => "tray-error.ico", _ => "tray-idle.ico" };
        icon.Icon = Load(name);
        menu.Update(statusLine, signedIn, canToggle, connected);
    }

    /// <summary>Follow the app's colour scheme (the menu reads the same tokens as the window).</summary>
    public void SetTheme(bool dark) => menu.ApplyTheme();

    public void Notify(string title, string text) => icon.ShowBalloonTip(8000, title, text, ToolTipIcon.Info);

    public void Dispose()
    {
        SystemEvents.DisplaySettingsChanged -= OnDisplaySettingsChanged;
        icon.Visible = false;
        icon.Dispose();
        menu.Dispose();
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

/// <summary>The right-click menu: a status line, then 打开 / 连接·断开 / 退出. Also drawn by the design preview.</summary>
public sealed class TrayMenu : IDisposable
{
    private readonly ToolStripMenuItem status = new("未登录") { Enabled = false };
    private readonly ToolStripMenuItem open = new("打开 AsterLink");
    private readonly ToolStripMenuItem toggle = new("连接");
    private readonly ToolStripMenuItem exit = new("退出");

    public TrayMenu(Action openWindow, Action toggleConnection, Action exitApp)
    {
        Strip = new ContextMenuStrip
        {
            ShowImageMargin = false,
            ShowCheckMargin = false,
            Padding = new Padding(4, 6, 4, 6),
            Font = new Font("Microsoft YaHei UI", 9f),
        };
        foreach (var item in new[] { status, open, toggle, exit }) item.Padding = new Padding(8, 5, 24, 5);
        status.Font = new Font(Strip.Font, FontStyle.Bold);
        Strip.Items.Add(status);
        Strip.Items.Add(new ToolStripSeparator());
        Strip.Items.Add(open);
        Strip.Items.Add(toggle);
        Strip.Items.Add(new ToolStripSeparator());
        Strip.Items.Add(exit);
        open.Click += (_, _) => openWindow();
        toggle.Click += (_, _) => toggleConnection();
        exit.Click += (_, _) => exitApp();
        Strip.HandleCreated += (_, _) => WindowBackdrop.RoundCorners(Strip.Handle);
        ApplyTheme();
    }

    public ContextMenuStrip Strip { get; }

    /// <summary>Every line of the menu, top to bottom (the design preview exports them for the copy guard).</summary>
    public IReadOnlyList<string> Texts => [status.Text ?? "", open.Text ?? "", toggle.Text ?? "", exit.Text ?? ""];

    public void Update(string statusLine, bool signedIn, bool canToggle, bool connected)
    {
        status.Text = statusLine;
        toggle.Text = connected ? "断开" : "连接";
        toggle.Enabled = signedIn && canToggle;
    }

    public void ApplyTheme()
    {
        var palette = TrayPalette.Current();
        Strip.Renderer = new TrayRenderer(palette);
        Strip.BackColor = palette.Surface;
        Strip.Invalidate();
    }

    public void Dispose() => Strip.Dispose();
}

/// <summary>The menu's colours, read from the current WPF theme (Theme/Light.xaml or Dark.xaml), translucent roles laid over the surface.</summary>
internal sealed record TrayPalette(Color Surface, Color Border, Color Text, Color TextMuted, Color Highlight, Color HighlightText)
{
    public static TrayPalette Current()
    {
        var surface = Read("SurfaceBrush", Color.White);
        return new TrayPalette(
            surface,
            Read("StrokeStrongBrush", Color.Gray, surface),
            Read("TextPrimaryBrush", Color.Black, surface),
            Read("TextSecondaryBrush", Color.DimGray, surface),
            Read("BrandSoftBrush", Color.LightGray, surface),
            Read("BrandBrush", Color.Black, surface));
    }

    private static Color Read(string key, Color fallback, Color? under = null)
    {
        if (System.Windows.Application.Current?.TryFindResource(key) is not System.Windows.Media.SolidColorBrush brush) return fallback;
        var c = brush.Color;
        if (c.A == 255 || under is not { } back) return Color.FromArgb(255, c.R, c.G, c.B);
        var a = c.A / 255.0;
        return Color.FromArgb(255, (int)Math.Round(c.R * a + back.R * (1 - a)), (int)Math.Round(c.G * a + back.G * (1 - a)), (int)Math.Round(c.B * a + back.B * (1 - a)));
    }
}

/// <summary>Flat, rounded highlight in the brand tint; status line in the muted text colour; thin separators.</summary>
internal sealed class TrayRenderer(TrayPalette palette) : ToolStripProfessionalRenderer(new TrayColors(palette))
{
    protected override void OnRenderToolStripBackground(ToolStripRenderEventArgs e)
    {
        using var fill = new SolidBrush(palette.Surface);
        e.Graphics.FillRectangle(fill, e.AffectedBounds);
    }

    protected override void OnRenderToolStripBorder(ToolStripRenderEventArgs e)
    {
        using var pen = new Pen(palette.Border);
        e.Graphics.DrawRectangle(pen, 0, 0, e.ToolStrip.Width - 1, e.ToolStrip.Height - 1);
    }

    protected override void OnRenderMenuItemBackground(ToolStripItemRenderEventArgs e)
    {
        if (!e.Item.Selected || !e.Item.Enabled) return;
        var bounds = new Rectangle(2, 1, e.Item.Width - 4, e.Item.Height - 2);
        using var path = Rounded(bounds, 4);
        using var fill = new SolidBrush(palette.Highlight);
        e.Graphics.SmoothingMode = System.Drawing.Drawing2D.SmoothingMode.AntiAlias;
        e.Graphics.FillPath(fill, path);
    }

    protected override void OnRenderItemText(ToolStripItemTextRenderEventArgs e)
    {
        e.TextColor = !e.Item.Enabled ? palette.TextMuted : e.Item.Selected ? palette.HighlightText : palette.Text;
        base.OnRenderItemText(e);
    }

    protected override void OnRenderSeparator(ToolStripSeparatorRenderEventArgs e)
    {
        using var pen = new Pen(palette.Border);
        var y = e.Item.Height / 2;
        e.Graphics.DrawLine(pen, 8, y, e.Item.Width - 8, y);
    }

    private static System.Drawing.Drawing2D.GraphicsPath Rounded(Rectangle r, int radius)
    {
        var path = new System.Drawing.Drawing2D.GraphicsPath();
        var d = radius * 2;
        path.AddArc(r.X, r.Y, d, d, 180, 90);
        path.AddArc(r.Right - d, r.Y, d, d, 270, 90);
        path.AddArc(r.Right - d, r.Bottom - d, d, d, 0, 90);
        path.AddArc(r.X, r.Bottom - d, d, d, 90, 90);
        path.CloseFigure();
        return path;
    }
}

internal sealed class TrayColors(TrayPalette palette) : ProfessionalColorTable
{
    public override Color ToolStripDropDownBackground => palette.Surface;
    public override Color MenuBorder => palette.Border;
    public override Color MenuItemBorder => palette.Highlight;
    public override Color MenuItemSelected => palette.Highlight;
    public override Color SeparatorDark => palette.Border;
    public override Color SeparatorLight => palette.Surface;
    public override Color ImageMarginGradientBegin => palette.Surface;
    public override Color ImageMarginGradientMiddle => palette.Surface;
    public override Color ImageMarginGradientEnd => palette.Surface;
}
