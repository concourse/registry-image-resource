FROM alpine
RUN echo symlinked > a
RUN ln -s a b
RUN rm b && echo replaced > b
