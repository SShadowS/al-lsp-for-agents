@description('Resource name for the App Insights resource.')
param name string

@description('Azure region. Use westeurope for GDPR alignment.')
param location string = 'westeurope'

@description('Daily ingestion cap in GB. 0.1 GB ~= 100MB ~= ~50k small events.')
param dailyCapGb int = 1

@description('Data retention in days. 30 is the lowest non-extended value.')
param retentionInDays int = 30

resource workspace 'Microsoft.OperationalInsights/workspaces@2022-10-01' = {
  name: '${name}-law'
  location: location
  properties: {
    sku: { name: 'PerGB2018' }
    retentionInDays: retentionInDays
    workspaceCapping: {
      dailyQuotaGb: dailyCapGb
    }
    features: {
      enableLogAccessUsingOnlyResourcePermissions: true
    }
  }
}

resource appInsights 'Microsoft.Insights/components@2020-02-02' = {
  name: name
  location: location
  kind: 'web'
  properties: {
    Application_Type: 'web'
    WorkspaceResourceId: workspace.id
    DisableIpMasking: false
    SamplingPercentage: 100
    publicNetworkAccessForIngestion: 'Enabled'
    publicNetworkAccessForQuery: 'Enabled'
  }
}

resource spikeAlert 'Microsoft.Insights/scheduledQueryRules@2023-03-15-preview' = {
  name: '${name}-spike'
  location: location
  properties: {
    severity: 2
    enabled: true
    evaluationFrequency: 'PT5M'
    windowSize: 'PT5M'
    scopes: [appInsights.id]
    criteria: {
      allOf: [
        {
          query: 'customEvents | where timestamp > ago(5m) | summarize count() by name'
          timeAggregation: 'Count'
          operator: 'GreaterThan'
          threshold: 5000
        }
      ]
    }
  }
}

output connectionString string = appInsights.properties.ConnectionString
