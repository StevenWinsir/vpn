package main

import (
	"bytes"
	"context"
	"core/managed"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"time"
)

var managedAccount = managed.NewCoordinator(newManagedTransport, func() {
	handleStopListener()
	handleCloseConnections()
	stopManagedPlatform()
}, managedProfiles, managedTotals)

func managedConnectionAllowed() bool { return managedAccount.Snapshot().CanConnect }

func allowManagedMethod(method CoreMethod) bool {
	switch method {
	case setupConfigMethod, updateConfigMethod, validateConfigMethod, getConfigMethod,
		changeProxyMethod, getProxiesMethod, getExternalProvidersMethod, getExternalProviderMethod,
		updateExternalProviderMethod, sideLoadExternalProviderMethod, updateGeoDataMethod, clearEffectMethod, asyncTestDelayMethod:
		return false
	case startListenerMethod:
		return managedConnectionAllowed()
	default:
		return true
	}
}

func resetManagedAccount() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = managedAccount.Reset(ctx)
}

type managedRevision struct {
	Generation uint64 `json:"generation"`
	Refresh    bool   `json:"refresh,omitempty"`
}

func managedArguments(call *MethodCall, result any, response MethodResponse) bool {
	if len(call.Arguments) == 0 || len(call.Arguments) > 4096 || bytes.Equal(call.Arguments, []byte("null")) {
		response.failure("invalid_arguments", "Invalid managed request", nil)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(call.Arguments))
	decoder.DisallowUnknownFields()
	if decoder.Decode(result) != nil || decoder.Decode(new(any)) != io.EOF {
		response.failure("invalid_arguments", "Invalid managed request", nil)
		return false
	}
	return true
}

func managedResponse(response MethodResponse, run func(context.Context) (managed.AccountSnapshot, error)) {
	go func() {
		defer func() {
			if recover() != nil {
				response.failure("managed_error", "Managed request failed", nil)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		state, err := run(ctx)
		if err != nil {
			response.failure(managed.PublicError(err), "Managed request failed", nil)
			return
		}
		response.success(state)
	}()
}

func init() {
	registerMethod(managedTunReadyMethod, withoutArguments(func(response MethodResponse) {
		response.success(isInit.Load() && (runtime.GOOS != "darwin" || os.Geteuid() == 0))
	}))
	registerMethod(managedConnectMethod, func(call *MethodCall, response MethodResponse) {
		var params struct {
			Generation      uint64 `json:"generation"`
			MixedPort       int    `json:"mixed_port"`
			RuntimeRevision uint64 `json:"runtime_revision"`
		}
		if !managedArguments(call, &params, response) {
			return
		}
		managedResponse(response, func(ctx context.Context) (managed.AccountSnapshot, error) {
			return managedAccount.Connect(ctx, params.Generation, params.MixedPort, params.RuntimeRevision)
		})
	})
	registerMethod(managedDisconnectMethod, func(call *MethodCall, response MethodResponse) {
		var params managedRevision
		if !managedArguments(call, &params, response) {
			return
		}
		managedResponse(response, func(ctx context.Context) (managed.AccountSnapshot, error) {
			return managedAccount.Disconnect(ctx, params.Generation)
		})
	})
	registerMethod(managedSelectProxyMethod, func(call *MethodCall, response MethodResponse) {
		var params struct {
			Generation      uint64 `json:"generation"`
			ConfigurationID string `json:"configuration_id"`
			Group           string `json:"group"`
			Proxy           string `json:"proxy"`
		}
		if !managedArguments(call, &params, response) {
			return
		}
		managedResponse(response, func(ctx context.Context) (managed.AccountSnapshot, error) {
			return managedAccount.SelectProxy(ctx, params.Generation, params.ConfigurationID, params.Group, params.Proxy)
		})
	})
	registerMethod(managedLoginMethod, func(call *MethodCall, response MethodResponse) {
		var params managed.LoginParams
		if !managedArguments(call, &params, response) {
			return
		}
		managedResponse(response, func(ctx context.Context) (managed.AccountSnapshot, error) {
			return managedAccount.Login(ctx, params)
		})
	})
	registerMethod(managedStatusMethod, func(call *MethodCall, response MethodResponse) {
		if len(call.Arguments) == 0 || bytes.Equal(call.Arguments, []byte("null")) {
			response.success(managedAccount.Snapshot())
			return
		}
		var params managedRevision
		if !managedArguments(call, &params, response) {
			return
		}
		if !params.Refresh {
			response.success(managedAccount.Snapshot())
			return
		}
		managedResponse(response, func(ctx context.Context) (managed.AccountSnapshot, error) {
			return managedAccount.Refresh(ctx, params.Generation)
		})
	})
	for _, method := range []CoreMethod{managedLoadConfigMethod, managedFlushMethod, managedLogoutMethod} {
		method := method
		registerMethod(method, func(call *MethodCall, response MethodResponse) {
			var params managedRevision
			if !managedArguments(call, &params, response) {
				return
			}
			managedResponse(response, func(ctx context.Context) (managed.AccountSnapshot, error) {
				switch method {
				case managedLoadConfigMethod:
					return managedAccount.LoadConfig(ctx, params.Generation)
				case managedFlushMethod:
					return managedAccount.Flush(ctx, params.Generation)
				default:
					return managedAccount.Logout(ctx, params.Generation)
				}
			})
		})
	}
	registerMethod(managedResetMethod, withoutArguments(func(response MethodResponse) {
		managedResponse(response, managedAccount.Reset)
	}))
}
