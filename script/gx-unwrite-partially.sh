#!/bin/bash

gx_prefix='gx'
target_files='*.go'
os_dist=`uname`

gx-go rewrite

declare -a unwrite_pkgs
unwrite_pkgs[0]='opentracing-go,github.com/opentracing/opentracing-go'
# unwrite_pkgs[1]='edf,github.com/abc/edf'

for i in ${unwrite_pkgs[@]}
do
    pkg=(${i//,/ })
    pkg_name=`echo ${pkg[0]} | sed 's/\//2/g'`
    pkg_path=`echo ${pkg[1]} | sed 's/\//\\\\\//g'`
    if [[ "$os_dist" == 'Darwin' ]]; then
        sed -i '' "s/$gx_prefix\/.*\/$pkg_name/$pkg_path/g" $target_files
    else
        sed -i "s/$gx_prefix\/.*\/$pkg_name/$pkg_path/g" $target_files
    fi
done

