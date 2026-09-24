import re

files = [
    "/home/arfian107/Projects/adatrack/backend/docker-compose.coolify.yml",
    "/home/arfian107/Projects/adatrack/backend/docker-compose.local.yml",
    "/home/arfian107/Projects/adatrack/backend/docker-compose.test.yml"
]

for file_path in files:
    try:
        with open(file_path, "r") as f:
            content = f.read()
        
        # Replace minio image
        content = content.replace("image: bitnami/minio:latest", "image: elestio/minio:latest")
        
        with open(file_path, "w") as f:
            f.write(content)
            
    except FileNotFoundError:
        pass

