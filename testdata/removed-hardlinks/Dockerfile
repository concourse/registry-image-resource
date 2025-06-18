FROM alpine
COPY Dockerfile /hardlink-test/
RUN ln /hardlink-test/Dockerfile /hardlink-test/hardlink-file
RUN rm /hardlink-test/Dockerfile
