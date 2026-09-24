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
        
        # Replace minio-setup image
        content = content.replace("image: minio/mc:latest", "image: alpine:latest")
        
        # Replace the entrypoint block
        old_entrypoint = """    entrypoint:
      - /bin/sh
      - -c
      - |
        /usr/bin/mc config host add myminio http://minio:9000 "${S3_ACCESS_KEY}" "${S3_SECRET_KEY}";
        /usr/bin/mc rm -r --force myminio/"${S3_BUCKET_NAME}" || true;
        /usr/bin/mc mb myminio/"${S3_BUCKET_NAME}" || true;
        /usr/bin/mc anonymous set public myminio/"${S3_BUCKET_NAME}";
        exit 0;"""

        new_entrypoint = """    entrypoint:
      - /bin/sh
      - -c
      - |
        apk add --no-cache curl
        curl -sO https://dl.min.io/client/mc/release/linux-amd64/mc
        chmod +x mc
        ./mc config host add myminio http://minio:9000 "${S3_ACCESS_KEY}" "${S3_SECRET_KEY}";
        ./mc rm -r --force myminio/"${S3_BUCKET_NAME}" || true;
        ./mc mb myminio/"${S3_BUCKET_NAME}" || true;
        ./mc anonymous set public myminio/"${S3_BUCKET_NAME}";
        exit 0;"""

        content = content.replace(old_entrypoint, new_entrypoint)
        
        with open(file_path, "w") as f:
            f.write(content)
            
    except FileNotFoundError:
        pass

