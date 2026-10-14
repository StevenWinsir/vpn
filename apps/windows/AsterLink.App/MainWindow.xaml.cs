using System.Windows;

namespace AsterLink.App;

public partial class MainWindow : Window
{
    public MainWindow(AppViewModel model)
    {
        InitializeComponent();
        DataContext = model;
        model.Confirm = text => MessageBox.Show(this, text, "AsterLink", MessageBoxButton.YesNo, MessageBoxImage.Question, MessageBoxResult.No) == MessageBoxResult.Yes;
    }

    public AppViewModel Model => (AppViewModel)DataContext;
}
