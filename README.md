![Interlink logo](./docs/static/img/interlink_logo.png)

## :information_source: Overview

### Introduction
InterLink aims to provide an abstraction for the execution of a Kubernetes pod on any remote resource capable of managing a Container execution lifecycle.
We target to facilitate the development of provider specific plugins, so the resource providers can leverage the power of virtual kubelet without a black belt in kubernetes internals.

The project consists of two main components:

- __A Kubernetes Virtual Node:__ based on the [VirtualKubelet](https://virtual-kubelet.io/) technology. Translating request for a kubernetes pod execution into a remote call to the interLink API server.
- __The interLink API server:__ a modular and pluggable REST server where you can create your own Container manager plugin (called sidecars), or use the existing ones: remote docker execution on a remote host, singularity Container on a remote SLURM batch system. This repo aims to maintain the SLURM sidecar as a standalone plugin.

The project got inspired by the [KNoC](https://github.com/CARV-ICS-FORTH/knoc) and [Liqo](https://github.com/liqotech/liqo/tree/master) projects, enhancing that with the implemention a generic API layer b/w the virtual kubelet component and the provider logic for the container lifecycle management.

## :information_source: Usage

### Requirements
- __[Our Kubernetes Virtual Node and the interLink API server](https://github.com/interTwin-eu/interLink)__
- __[The Go programming language](https://go.dev/doc/install)__ (to build binaries)
- __[Docker Engine](https://docs.docker.com/engine/)__ (optional)

Note: if you want a quick start setup (using a Docker container), Go is not necessary

### Quick Start
Just run:
```bash
cd docker && docker compose up -d
```

### Building binaries
It is of course possible to use binaries as a standalone application. Just run 
```bash
make all
```
and you will be able to find the built slurm-sd binary inside the bin directory. Before executing it, remember to check if the configuration file is correctly set according to your needs. You can find an example one under examples/config/InterLinkConfig.yaml. Do not forget to set the INTERLINKCONFIGPATH environment variable to point to your config.

### :information_source: Annotations
It is possible to specify Annotations when submitting Pods to the K8S cluster. A list of all Annotations follows:
| Annotation    | Description|
|--------------|------------|
| slurm-job.vk.io/singularity-commands | Used to add specific Commands to be executed before the actual SLURM Job starts. It adds Commands on the Singularity exection line, in the SLURM bastch file |
| slurm-job.vk.io/pre-exec | Used to add commands to be executed before the Job starts. It adds a command in the SLURM batch file after the #SBATCH directives |
| slurm-job.vk.io/singularity-mounts | Used to add mountpoints to the Singularity Containers |
| slurm-job.vk.io/singularity-options | Used to specify Singularity arguments |
| slurm-job.vk.io/image-root | Used to specify the root path of the Singularity Image |
| slurm-job.vk.io/flags | Used to specify SLURM flags. These flags will be added to the SLURM script in the form of #SBATCH flag1, #SBATCH flag2, etc |
| slurm-job.vk.io/mpi-flags | Used to prepend "mpiexec -np $SLURM_NTASKS \*flags\*" to the Singularity Execution |

### :information_source: InterLink Config file
Detailed explanation of the InterLink config file key values. Edit the config file before running the binary or before building the docker image (`docker compose up -d --build --force-recreate` will recreate and re-run the updated image)
| Key         | Value     |
|--------------|-----------|
| InterlinkURL | the URL to allow the Virtual Kubelet to contact the InterLink module. |
| SidecarURL | the URL to allow InterLink to communicate with the Sidecar module (docker, slurm, etc). Do not specify port here |
| InterlinkPort | the Interlink listening port. InterLink and VK will communicate over this port. |
| SidecarPort | the sidecar listening port. Sidecar and Interlink will communicate on this port. Set $SIDECARPORT environment variable to specify a custom one |
| SbatchPath | path to your Slurm's sbatch binary |
| ScancelPath | path to your Slurm's scancel binary | 
| CommandPrefix | here you can specify a prefix for the programmatically generated script (for the slurm plugin). Basically, if you want to run anything before the script itself, put it here. |
| ExportPodData | Set it to true if you want to export Pod's ConfigMaps and Secrets as mountpoints in your Singularity Container |
| DataRootFolder | Specify where to store the exported ConfigMaps/Secrets locally |
| Namespace | Namespace where Pods in your K8S will be registered |
| Tsocks | true or false values only. Enables or Disables the use of tsocks library to allow proxy networking. Only implemented for the Slurm sidecar at the moment. |
| TsocksPath | path to your tsocks library. |
| TsocksLoginNode | specify an existing node to ssh to. It will be your "window to the external world" |
| BashPath | Path to your Bash shell |
| VerboseLogging | Enable or disable Debug messages on logs. True or False values only |
| ErrorsOnlyLogging | Specify if you want to get errors only on logs. True or false values only |

### :information_source: Environment Variables list
Here's the complete list of every customizable environment variable. When specified, it overwrites the listed key within the InterLink config file.

| Env         | Value     |
|--------------|-----------|
| VK_CONFIG_PATH | VK config file path |
| INTERLINKURL | the URL to allow the Virtual Kubelet to contact the InterLink module. Do not specify a port here. Overwrites InterlinkURL. |
| INTERLINKPORT | the InterLink listening port. InterLink and VK will communicate over this port. Overwrites InterlinkPort. |
| INTERLINKCONFIGPATH | your InterLink config file path. Default is `./kustomizations/InterLinkConfig.yaml` |
| SIDECARURL | the URL to allow InterLink to communicate with the Sidecar module (docker, slurm, etc). Do not specify port here. Overwrites SidecarURL. |
| SIDECARPORT | the Sidecar listening port. Docker default is 4000, Slurm default is 4001. |
| SBATCHPATH | path to your Slurm's sbatch binary. Overwrites SbatchPath. |
| SCANCELPATH | path to your Slurm's scancel binary. Overwrites ScancelPath. |
| VKTOKENFILE | path to a file containing your token fot OAuth2 proxy authentication. Overwrites VKTokenFile. |
| SHARED_FS | set this env to "true" to save configmaps values inside files directly mounted to Singularity containers instead of using ENVS to create them later |
| CUSTOMKUBECONF | path to a service account kubeconfig |
| TSOCKS | true or false, to use tsocks library allowing proxy networking. Working on Slurm sidecar at the moment. Overwrites Tsocks. |
| TSOCKSPATH | path to your tsocks library. Overwrites TsocksPath. |