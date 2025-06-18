# System-Monitor-Go

## Config File Structure Example
```
{
  "collectionInterval": 3,
  "sendingInterval": 3,
  "serverUrl": "url | ip:port",
  "localKey": "will be auto generationed",
  "userKey": "your obscura key",
  "collectors": {
    "cpu": {
      "cacheDurationSeconds": 3,
      "backgroundSamplingMillis": 500,
      "backgroundFrequencySeconds": 2,
      "directSamplingMillis": 750
    },
    "system": {
      "cacheDurationSeconds": 3,
      "updateFrequencySeconds": 2
    },
    "memory": {
      "cacheDurationSeconds": 3,
      "updateFrequencySeconds": 2
    },
    "disk": {
      "cacheDurationSeconds": 3,
      "updateFrequencySeconds": 2
    },
    "network": {
      "cacheDurationSeconds": 3,
      "updateFrequencySeconds": 2
    },
    "docker": {
      "cacheDurationSeconds": 3,
      "updateFrequencySeconds": 2
    },
    "process": {
      "cacheDurationSeconds": 3,
      "updateFrequencySeconds": 2
    },
    "service": {
      "cacheDurationSeconds": 3,
      "updateFrequencySeconds": 2
    }
  }
}
```

save configs/config.json