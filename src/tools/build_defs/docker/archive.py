# Copyright 2015 Google Inc. All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
"""Archive manipulation library for the Docker rules."""

import os
from StringIO import StringIO
import tarfile


class SimpleArFile(object):
  """A simple AR file reader.

  This enable to read AR file (System V variant) as described
  in https://en.wikipedia.org/wiki/Ar_(Unix).

  The standard usage of this class is:

  with SimpleArFile(filename) as ar:
    nextFile = ar.next()
    while nextFile:
      print nextFile.filename
      nextFile = ar.next()

  Upon error, this class will raise a ArError exception.
  """
  # TODO(dmarting): We should use a standard library instead but python 2.7
  #   does not have AR reading library.

  class ArError(Exception):
    pass

  class SimpleArFileEntry(object):
    """Represent one entry in a AR archive.

    Attributes:
      filename: the filename of the entry, as described in the archive.
      timestamp: the timestamp of the file entry.
      owner_id, group_id: numeric id of the user and group owning the file.
      mode: unix permission mode of the file
      size: size of the file
      data: the content of the file.
    """

    def __init__(self, f):
      self.filename = f.read(16).strip()
      if self.filename.endswith('/'):  # SysV variant
        self.filename = self.filename[:-1]
      self.timestamp = int(f.read(12).strip())
      self.owner_id = int(f.read(6).strip())
      self.group_id = int(f.read(6).strip())
      self.mode = int(f.read(8).strip(), 8)
      self.size = int(f.read(10).strip())
      pad = f.read(2)
      if pad != '\x60\x0a':
        raise SimpleArFile.ArError('Invalid AR file header')
      self.data = f.read(self.size)

  MAGIC_STRING = '!<arch>\n'

  def __init__(self, filename):
    self.filename = filename

  def __enter__(self):
    self.f = open(self.filename, 'rb')
    if self.f.read(len(self.MAGIC_STRING)) != self.MAGIC_STRING:
      raise self.ArError('Not a ar file: ' + self.filename)
    return self

  def __exit__(self, t, v, traceback):
    self.f.close()

  def next(self):
    """Read the next file. Returns None when reaching the end of file."""
    # AR sections are two bit aligned using new lines.
    if self.f.tell() % 2 != 0:
      self.f.read(1)
    # An AR sections is at least 60 bytes. Some file might contains garbage
    # bytes at the end of the archive, ignore them.
    if self.f.tell() > os.fstat(self.f.fileno()).st_size - 60:
      return None
    return self.SimpleArFileEntry(self.f)


class TarFileWriter(object):
  """A wrapper to write tar files."""

  def __init__(self, name):
    self.tar = tarfile.open(name=name, mode='w')

  def __enter__(self):
    return self

  def __exit__(self, t, v, traceback):
    self.close()

  def add_file(self, name, kind=tarfile.REGTYPE, content=None, link=None,
               file_content=None, uid=0, gid=0, uname='', gname='', mtime=0,
               mode=None):
    """Add a file to the current tar.

    Args:
      name: the name of the file to add.
      kind: the type of the file to add, see tarfile.*TYPE.
      content: a textual content to put in the file.
      link: if the file is a link, the destination of the link.
      file_content: file to read the content from. Provide either this
          one or `content` to specifies a content for the file.
      uid: owner user identifier.
      gid: owner group identifier.
      uname: owner user names.
      gname: owner group names.
      mtime: modification time to put in the archive.
      mode: unix permission mode of the file, default 0644 (0755).
    """
    if not name.startswith('.') and not name.startswith('/'):
      name = './' + name
    tarinfo = tarfile.TarInfo(name)
    tarinfo.mtime = mtime
    tarinfo.uid = uid
    tarinfo.gid = gid
    tarinfo.uname = uname
    tarinfo.gname = gname
    tarinfo.type = kind
    if mode is None:
      tarinfo.mode = 0644 if kind == tarfile.REGTYPE else 0755
    else:
      tarinfo.mode = mode
    if link:
      tarinfo.linkname = link
    if content:
      tarinfo.size = len(content)
      self.tar.addfile(tarinfo, StringIO(content))
    elif file_content:
      with open(file_content, 'rb') as f:
        tarinfo.size = os.fstat(f.fileno()).st_size
        self.tar.addfile(tarinfo, f)
    else:
      self.tar.addfile(tarinfo)

  def add_tar(self, tar, rootuid=None, rootgid=None,
              numeric=False, name_filter=None):
    """Merge a tar content into the current tar, stripping timestamp.

    Args:
      tar: the name of tar to extract and put content into the current tar.
      rootuid: user id that we will pretend is root (replaced by uid 0).
      rootgid: group id that we will pretend is root (replaced by gid 0).
      numeric: set to true to strip out name of owners (and just use the
          numeric values).
      name_filter: filter out file by names. If not none, this method will be
          called for each file to add, given the name and should return true if
          the file is to be added to the final tar and false otherwise.
    """
    compression = os.path.splitext(tar)[-1][1:]
    if compression == 'tgz':
      compression = 'gz'
    elif compression == 'bzip2':
      compression = 'bz2'
    elif compression == 'lzma':
      compression = 'xz'
    elif compression not in ['gz', 'bz2', 'xz']:
      compression = ''
    if compression == 'xz':
      # Python 2 does not support lzma, our py3 support is terrible so let's
      # just hack around.
      # Note that we buffer the file in memory and it can have an important
      # memory footprint but it's probably fine as we don't use them for really
      # large files.
      # TODO(dmarting): once our py3 support gets better, compile this tools
      # with py3 for proper lzma support.
      f = StringIO(os.popen('cat %s | xzcat' % tar).read())
      intar = tarfile.open(fileobj=f, mode='r:')
    else:
      intar = tarfile.open(name=tar, mode='r:' + compression)
    for tarinfo in intar:
      if name_filter is None or name_filter(tarinfo.name):
        tarinfo.mtime = 0
        if rootuid is not None and tarinfo.uid == rootuid:
          tarinfo.uid = 0
          tarinfo.uname = 'root'
        if rootgid is not None and tarinfo.gid == rootgid:
          tarinfo.gid = 0
          tarinfo.gname = 'root'
        if numeric:
          tarinfo.uname = ''
          tarinfo.gname = ''
        name = tarinfo.name
        if not name.startswith('/') and not name.startswith('.'):
          tarinfo.name = './' + name

        if tarinfo.isfile():
          self.tar.addfile(tarinfo, intar.extractfile(tarinfo.name))
        else:
          self.tar.addfile(tarinfo)
    intar.close()

  def close(self):
    """Close the output tar file.

    This class should not be used anymore after calling that method.
    """
    self.tar.close()
