using System.Collections.Concurrent;
using System.IO.Pipes;
using AsterLink.Platform;
using AsterLink.Shared.Service;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;

namespace AsterLink.Service;

/// <summary>Accepts GUI connections on the secured pipe, one session object each. Does nothing else.</summary>
public sealed class PipeListener(ILoggerFactory loggers, IHostApplicationLifetime lifetime) : BackgroundService
{
    private readonly ConcurrentDictionary<ClientSession, byte> sessions = new();
    private readonly ILogger logger = loggers.CreateLogger<PipeListener>();

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        var runtime = ServiceRuntime.Create(loggers, out var refusal);
        if (runtime is null)
        {
            // Not installed where it must be: stop instead of serving a pipe from an unprotected copy.
            logger.LogError("Service not started: {Reason}", refusal);
            lifetime.StopApplication();
            return;
        }

        var first = true;
        while (!stoppingToken.IsCancellationRequested)
        {
            NamedPipeServerStream pipe;
            try { pipe = ServiceEndpoint.Create(ServiceConstants.PipeName, first); }
            catch (Exception error) when (first && error is IOException or UnauthorizedAccessException)
            {
                // Another process already owns the pipe name: never serve beside an impostor.
                logger.LogError("The pipe name is already in use ({Message}); the service will not start", error.Message);
                lifetime.StopApplication();
                return;
            }
            first = false;

            try { await pipe.WaitForConnectionAsync(stoppingToken).ConfigureAwait(false); }
            catch (OperationCanceledException) { await pipe.DisposeAsync().ConfigureAwait(false); break; }
            catch (IOException) { await pipe.DisposeAsync().ConfigureAwait(false); continue; }

            var session = new ClientSession(pipe, runtime);
            sessions[session] = 0;
            var run = session.RunAsync(stoppingToken);
            _ = run.ContinueWith(_ => sessions.TryRemove(session, out byte _), TaskScheduler.Default);
        }

        // Service stopping: every Core goes down with it.
        await Task.WhenAll(sessions.Keys.Select(s => s.CloseAsync())).ConfigureAwait(false);
        logger.LogInformation("AsterLink service stopped");
    }
}
