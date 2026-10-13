using System.Windows;

namespace AsterLink.App;

public partial class MainWindow : Window
{
    public MainWindow()
    {
        InitializeComponent();
        // Look at the service as soon as the window is up; the card shows "检测中…" until then.
        Loaded += async (_, _) => await ((MainViewModel)DataContext).Service.RefreshAsync();
    }
}
