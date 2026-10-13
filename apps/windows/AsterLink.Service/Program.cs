using AsterLink.Service;
using AsterLink.Shared.Platform;
using AsterLink.Shared.Service;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

// `--probe` prints the platform facts and the shared-contract self-check and exits, so CI can
// prove the real binary starts on a real Windows x64 host without installing a service.
if (args.Contains("--probe"))
{
    var report = PlatformReport.Capture("AsterLink.Service");
    var checks = FoundationSelfCheck.Run();
    Console.WriteLine(report.ToJson());
    Console.WriteLine(FoundationSelfCheck.ToJson(checks));
    return checks.All(c => c.Passed) ? 0 : 1;
}

// install / uninstall / repair / status: the development installer (see ServiceVerbs).
if (ServiceVerbs.IsVerb(args)) return ServiceVerbs.Run(args);

if (args.Length != 0)
{
    // The service takes no arguments: nothing a caller could pass is ever interpreted.
    Console.Error.WriteLine("AsterLink.Service.exe takes no arguments when run as a service");
    return ServiceConstants.ExitCodes.Usage;
}

var builder = Host.CreateApplicationBuilder(args);
builder.Services.AddWindowsService(options => options.ServiceName = ServiceConstants.ServiceName);
builder.Services.AddHostedService<PipeListener>();
await builder.Build().RunAsync();
return 0;
