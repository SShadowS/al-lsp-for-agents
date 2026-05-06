# Azure App Insights Infra

`appinsights.bicep` provisions the telemetry destination for AL LSP for Agents.

## Deploy (one-time)

```powershell
$rg = "al-lsp-telemetry-rg"
az group create --name $rg --location westeurope
az deployment group create --resource-group $rg --template-file appinsights.bicep --parameters name=al-lsp-telemetry
```

The output `connectionString` is the value to feed to the build via `AL_LSP_APPINSIGHTS_CONNSTR` (release CI secret).

## Validation

After deploy, run a single forged envelope to confirm Daily Cap behavior. The wrapper's `AL_LSP_TELEMETRY_DUMP=<path>` mode is the safest way to inspect what would have shipped without actually sending.

## Rotation

If the connection string leaks, regenerate the App Insights resource:

1. Delete the resource group, redeploy.
2. Update CI secret `AL_LSP_APPINSIGHTS_CONNSTR` with the new value.
3. Tag a patch release. Old binaries point at the now-defunct endpoint; Daily Cap on the old resource limits damage.
