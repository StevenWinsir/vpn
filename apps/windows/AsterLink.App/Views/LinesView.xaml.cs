using System.Windows;
using System.Windows.Controls;

namespace AsterLink.App.Views;

public partial class LinesView : UserControl
{
    public LinesView() => InitializeComponent();

    private void OnClearSearch(object sender, RoutedEventArgs e)
    {
        if (DataContext is AppViewModel model) model.Search = "";
        SearchBox.Focus();
    }
}
