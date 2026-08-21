variable "node_count" {
  description = "Number of edge nodes to provision"
  type        = number
}

variable "cpu" {
  description = "CPUs per edge node"
  type        = number
}

variable "memory_mb" {
  description = "Memory in MB per edge node"
  type        = number
}

variable "disk_gb" {
  description = "Disk size in GB per edge node"
  type        = number
}

variable "image" {
  description = "Operating system image for edge nodes"
  type        = string
}

variable "base_name" {
  description = "Sanitized base name for resource identifiers"
  type        = string
}

variable "output_dir" {
  description = "Directory where node descriptors are written"
  type        = string
}
