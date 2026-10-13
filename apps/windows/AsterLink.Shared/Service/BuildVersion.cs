using System.Reflection;
using System.Text.RegularExpressions;

namespace AsterLink.Shared.Service;

/// <summary>The product version, taken from Directory.Build.props through the assembly version.</summary>
public static partial class BuildVersion
{
    [GeneratedRegex(@"^\d{1,4}\.\d{1,4}\.\d{1,4}$")]
    private static partial Regex Pattern();

    public static bool IsWellFormed(string? version) => version is not null && Pattern().IsMatch(version);

    /// <summary>Major.Minor.Build of this assembly. Both GUI and service reference Shared, so they agree by construction.</summary>
    public static string Current
    {
        get
        {
            var v = typeof(BuildVersion).Assembly.GetName().Version ?? new Version(0, 0, 0);
            return $"{v.Major}.{v.Minor}.{Math.Max(v.Build, 0)}";
        }
    }
}
