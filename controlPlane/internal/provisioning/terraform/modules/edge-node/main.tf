resource "local_file" "node" {
  count         = var.node_count
  filename      = path.join(var.output_dir, "node-${count.index + 1}.txt")
  content       = <<EOT
node_name="${var.base_name}-${count.index + 1}"
node_cpu=${var.cpu}
node_memory_mb=${var.memory_mb}
node_disk_gb=${var.disk_gb}
node_image="${var.image}"
EOT
}