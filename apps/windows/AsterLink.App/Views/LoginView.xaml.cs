using System.Windows;
using System.Windows.Automation;
using System.Windows.Controls;

namespace AsterLink.App.Views;

public partial class LoginView : UserControl
{
    private bool syncing;

    public LoginView()
    {
        InitializeComponent();
        IsVisibleChanged += (_, _) =>
        {
            if (!IsVisible) return;
            // Land the cursor where the user has to type next.
            Dispatcher.BeginInvoke(() =>
            {
                if (DataContext is AppViewModel { Email.Length: > 0 }) PasswordField().Focus();
                else EmailBox.Focus();
            }, System.Windows.Threading.DispatcherPriority.Input);
        };
    }

    private bool Revealed => PasswordText.Visibility == Visibility.Visible;

    private Control PasswordField() => Revealed ? PasswordText : PasswordBox;

    /// <summary>"显示密码": the same characters move into a plain text box and back; nothing is stored anywhere else.</summary>
    private void OnReveal(object sender, RoutedEventArgs e)
    {
        var show = !Revealed;
        syncing = true;
        if (show) PasswordText.Text = PasswordBox.Password;
        else PasswordBox.Password = PasswordText.Text;
        syncing = false;
        PasswordText.Visibility = show ? Visibility.Visible : Visibility.Collapsed;
        PasswordBox.Visibility = show ? Visibility.Collapsed : Visibility.Visible;
        RevealGlyph.Tag = FindResource(show ? "IconEyeOff" : "IconEye");
        var label = show ? "隐藏密码" : "显示密码";
        AutomationProperties.SetName(RevealButton, label);
        RevealButton.ToolTip = label;
        PasswordField().Focus();
        if (show) PasswordText.CaretIndex = PasswordText.Text.Length;
    }

    private void OnPasswordChanged(object sender, RoutedEventArgs e)
    {
        if (syncing || !Revealed) return;
        syncing = true;
        PasswordText.Text = PasswordBox.Password;
        syncing = false;
    }

    private void OnPasswordTextChanged(object sender, TextChangedEventArgs e)
    {
        if (syncing || !Revealed) return;
        syncing = true;
        PasswordBox.Password = PasswordText.Text;
        syncing = false;
    }

    private async void OnSubmit(object sender, RoutedEventArgs e)
    {
        if (DataContext is not AppViewModel model) return;
        // The password is read once, handed to the one login call, and both boxes are cleared on success.
        var password = Revealed ? PasswordText.Text : PasswordBox.Password;
        try
        {
            await model.SignInAsync(password);
            if (model.IsSignedIn)
            {
                syncing = true;
                PasswordBox.Clear();
                PasswordText.Clear();
                syncing = false;
            }
        }
        catch (Exception error) { model.ReportFailure(error); }
    }
}
