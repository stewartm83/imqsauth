
set tag=%1
docker build . -t imqs/auth:%tag%
docker push imqs/auth