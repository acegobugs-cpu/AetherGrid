# The edge-node module declares the infrastructure characteristics of one
# AETHER-GRID edge node. It is provider-agnostic: the local provider is used so
# the full plan/apply/destroy lifecycle works without a cloud account. A real
# provider (AWS, GCP, libvirt) can replace this module without touching the
# control plane.

resource "local_file" "node" {
  count    = var.node_count
  filename = "${var.output_dir}/${var.base_name}-${count.index + 1}"
  content  = jsonencode({
    name      = "${var.base_name}-${count.index + 1}"
    cpu       = var.cpu
    memory_mb = var.memory_mb
    disk_gb   = var.disk_gb
    image     = var.image
    provider  = "local"
  })
}

output "node_count" {
  value = var.node_count
}

output "node_ids" {
  value = local_file.node[*].id
}

output "node_names" {
  value = [for i in range(var.node_count) : "${var.base_name}-${i + 1}"]
}

output "node_addresses" {
  value = [for i in range(var.node_count) : "10.0.0.${i + 1}"]
}
