# Typesense Kubernetes Node Resolver
*A sidecar container for Typesense that helps operate HA Typesense clusters in Kubernetes.*

### Problem
When restarting/upgrading Typesense nodes in a high availability cluster running in Kubernetes, DNS entries of the stateful set do not get resolved again with the new IP, causing the pod to be unable to rejoin the cluster. 

### Solution
Instead of storing the `nodeslist` values in a configmap (as a file through a volume), the `nodeslist` volume is configured with `emptyDir` and the sidecar container dynamically updates the values of the nodelist. 

To do this it watches endpoints in the configured namespace for changes and sets IPs as node values rather than using DNS. 

```
typesense-0.ts.typesense.svc.cluster.local:8107:8108
```
**format**: statefulSetName-0.serviceName.nameSpace...

 _usually preferred_

VS.

```
10.244.1.215:8107:8108
``` 
_created by the sidecar, solves the DNS resolution issue_


## Context

Normally you'd have a configmap like this

```
apiVersion: v1
kind: ConfigMap
metadata:
  name: nodeslist
  namespace: typesense
data:
  nodes: "typesense-0.ts.typesense.svc.cluster.local:8107:8108,typesense-1.ts.typesense.svc.cluster.local:8107:8108,typesense-2.ts.typesense.svc.cluster.local:8107:8108"
```

With a `command` for the Typesense container (located within the StatefulSet definition) which points to that information (as a file)

```
command:
    - "/opt/typesense-server"
    - "-d"
    - "/usr/share/typesense/data"
    - "--api-port"
    - "8108"
    - "--peering-port"
    - "8107"
    - "--nodes"
    - "/usr/share/typesense/nodes"
```

Along with the volume mount

```
volumeMounts:
    - name: nodeslist
        mountPath: /usr/share/typesense
    - name: data
        mountPath: /usr/share/typesense/data
```

And finally the `volumes`

```
volumes:
    - name: nodeslist
        configMap:
        name: nodeslist
        items:
            - key: nodes
            path: nodes
```

## Usage & Configuration

0) Build your own image with `docker build . -t yourRepo/typesense-node-resolver:latest` (and push that to your repo) or use `alasano/typesense-node-resolver`

1) You can discard the `configMap` entirely, unless you use it for other values. Normally you're using a `secret` for the API key anyways.

2) Leave the `volumeMounts` as they were.

3) Change the `volumes` to the following:

```
volumes:
    - name: nodeslist
        emptyDir: {}
```

4) You'll need to create a `ServiceAccount` , `Role` and `RoleBinding` for the sidecar.

```
apiVersion: v1
kind: ServiceAccount
metadata:
  name: typesense-service-account
  namespace: typesense
# imagePullSecrets:
#   - name: your-image-pull-secret
```

```
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: typesense-role
  namespace: typesense
rules:
- apiGroups: [""]
  resources: ["endpoints"]
  verbs: ["watch", "list"]
```

```
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: typesense-role-binding
  namespace: typesense
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: typesense-role
subjects:
- kind: ServiceAccount
  name: typesense-service-account
  namespace: typesense
```

5) Finally, you can add the sidecar to the pod containers and set one of a handful of configuration parameters

```
- name: ts-node-resolver
    image: alasano/typesense-node-resolver
    command:
        - "/opt/tsns"
        - "-namespace=someOtherNamespace"
    volumeMounts:
        - name: nodeslist
        mountPath: /usr/share/typesense

```

* `-namespace=NS` // _Namespace in which Typesense is installed (default: typesense)_
* `-service=SVC` // _Service for which to retrieve endpoints (default: ts)_
* `-nodes-file=PATH` // _The location to write the nodes list to (default: /usr/share/typesense/nodes)
* `-peer-port=PORT` // _Port on which Typesense peering service listens (default: 8107)_
* `-api-port=PORT` // _Port on which Typesense API service listens (default: 8108)_

### Full Example

You can see a full example in [typesense.yaml](/typesense.yml)


_All credit for initial implementation goes to [Elliot Wright](https://github.com/seeruk) - Forked from [github.com/seeruk/tsns](https://github.com/seeruk/tsns)_