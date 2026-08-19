variable "node_count" {
  description = "Number of nodes to provision"
  type        = number
}

variable "cpu" {
  description = "CPU cores per node"
  type        = number
}

variable "memory_mb" {
  description = "Memory in MB per node"
  type        = number
}

variable "disk_gb" {
  description = "Disk size in GB per node"
  type        = number
}

variable "image" {
  description = "Operating system image"
  type        = string
}

variable "base_name" {
  description = "Base name for node naming"
  type        = string
}

variable "output_dir" {
  description = "Directory where node files are created"
  type        = string
}