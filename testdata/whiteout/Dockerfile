FROM alpine
RUN mkdir top-dir-1
RUN touch top-dir-1/nested-file
RUN mkdir top-dir-1/nested-dir
RUN touch top-dir-1/nested-dir/file-gone
RUN touch top-dir-1/nested-dir/file-recreated
RUN touch top-dir-1/nested-dir/file-then-dir
RUN rm -rf top-dir-1/nested-dir
RUN mkdir top-dir-1/nested-dir
RUN touch top-dir-1/nested-dir/file-here
RUN touch top-dir-1/nested-dir/file-recreated
RUN mkdir top-dir-1/nested-dir/file-then-dir
RUN mkdir top-dir-2
RUN touch top-dir-2/file-gone
RUN mkdir top-dir-2/nested-dir-gone
RUN touch top-dir-2/nested-dir-gone/nested-file-gone
RUN rm -rf top-dir-2
RUN mkdir -p top-dir-3/nested-dir-gone
RUN rm -r top-dir-3 && mkdir -p top-dir-3/nested-dir
RUN mkdir top-dir-4 && chown 1000:1000 top-dir-4

# resulting file tree should be:
# /top-dir-1/nested-file
# /top-dir-1/nested-dir/file-here
# /top-dir-1/nested-dir/file-recreated
# /top-dir-1/nested-dir/file-then-dir
# /top-dir-3/nested-dir
# /top-dir-4
