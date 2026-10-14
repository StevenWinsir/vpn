using System.Windows;
using System.Windows.Controls;

namespace AsterLink.App.Views;

public partial class LoginView : UserControl
{
    public LoginView()
    {
        InitializeComponent();
        IsVisibleChanged += (_, _) =>
        {
            if (!IsVisible) return;
            // Land the cursor where the user has to type next.
            Dispatcher.BeginInvoke(() =>
            {
                if (DataContext is AppViewModel { Email.Length: > 0 }) PasswordBox.Focus();
                else EmailBox.Focus();
            }, System.Windows.Threading.DispatcherPriority.Input);
        };
    }

    private async void OnSubmit(object sender, RoutedEventArgs e)
    {
        if (DataContext is not AppViewModel model) return;
        // The password is read once, handed to the one login call, and the box is cleared on success.
        var password = PasswordBox.Password;
        try
        {
            await model.SignInAsync(password);
            if (model.IsSignedIn) PasswordBox.Clear();
        }
        catch (Exception error) { model.ReportFailure(error); }
    }
}
