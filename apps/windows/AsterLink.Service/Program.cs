using AsterLink.Service;
using AsterLink.Shared.Platform;

// Round 1 skeleton. The service owns no privileged behaviour yet: no pipe, no Core, no TUN.
// `--probe` prints the platform facts and the shared-contract self-check and exits, so CI
// can prove the real binary starts on a real Windows x64 host without installing a service.
if (args.Contains("--probe"))
{
    var report = PlatformReport.Capture("AsterLink.Service");
    var checks = FoundationSelfCheck.Run();
    Console.WriteLine(report.ToJson());
    Console.WriteLine(FoundationSelfCheck.ToJson(checks));
    return checks.All(c => c.Passed) ? 0 : 1;
}

var builder = Host.CreateApplicationBuilder(args);
builder.Services.AddWindowsService(options => options.ServiceName = ServiceIdentity.Name);
builder.Services.AddHostedService<IdleSupervisor>();
await builder.Build().RunAsync();
return 0;
