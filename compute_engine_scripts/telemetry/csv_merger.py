#!/usr/bin/env python
# Copyright (c) 2013 The Chromium Authors. All rights reserved.
# Use of this source code is governed by a BSD-style license that can be
# found in the LICENSE file.

"""Python utility to merge many CSV files into a single file."""

import csv
import glob
import optparse
import os
import sys


# TODO(rmistry): Should this be 'None' instead?
EMPTY_VALUE = 0.0


class CsvMerger(object):
  """Class that merges many CSV files into a single file."""

  def __init__(self, csv_dir, output_csv_name):
    """Constructs a CsvMerge instance."""
    self._csv_dir = csv_dir
    self._output_csv_name = output_csv_name
    self._csv_dict = {}

  def Merge(self):
    """Method that does the CSV merging."""
    # Counter that keeps track of how many entries have been populated so far.
    total_entries = 0
    for csv_file in glob.glob(os.path.join(self._csv_dir, '*.csv')):
      csv_full_path = os.path.join(self._csv_dir, csv_file)

      # If the CSV file is empty then move on to the next one.
      if os.path.getsize(csv_full_path) == 0:
        continue

      csv_reader = csv.reader(open(csv_full_path, 'r'))
      headers = csv_reader.next()
      remaining_lines = []
      for remaining_line in csv_reader:
        remaining_lines.append(remaining_line)

      header_index = 0
      entries_in_curr_csv = 0
      for header in headers:
        entries_in_curr_csv = 0
        if header in self._csv_dict:
          entries = self._csv_dict[header]
          for csv_entries in remaining_lines:
            csv_entry = csv_entries[header_index]
            entries.append(csv_entry)
            entries_in_curr_csv += 1
        else:
          entries = []
          # Populate the previous entries with the EMPTY_VALUE since this is
          # the first time we have encountered this header.
          for _index in range(0, total_entries):
            entries.append(EMPTY_VALUE)
          for csv_entries in remaining_lines:
            csv_entry = csv_entries[header_index]
            entries.append(csv_entry)
            entries_in_curr_csv += 1
          self._csv_dict[header] = entries
        header_index += 1

      total_entries += entries_in_curr_csv
      # Do a loop here for headers that are not in the currect CSV file and add
      # EMPTY_VALUEs for them.
      for missing_header in set(self._csv_dict) - set(headers):
        for _unused in xrange(0, entries_in_curr_csv):
          self._csv_dict[missing_header].append(EMPTY_VALUE)

    # Output the constructed dictionary to CSV.
    file_writer = csv.writer(
        open(os.path.join(self._csv_dir, self._output_csv_name), 'w'))
    file_writer.writerow(self._csv_dict.keys())
    for entry_index in range(0, total_entries):
      csv_row = []
      for header in self._csv_dict.keys():
        csv_row.append(self._csv_dict[header][entry_index])
      file_writer.writerow(csv_row)


if '__main__' == __name__:
  option_parser = optparse.OptionParser()
  option_parser.add_option(
      '', '--csv_dir',
      help='Directory that contains the csv files to be merged. This directory'
           ' will also contain the merged CSV.')
  option_parser.add_option(
      '', '--output_csv_name',
      help='The name of the resultant merged CSV. It will be outputted to the '
           '--csv_dir')
  options, unused_args = option_parser.parse_args()
  if not options.csv_dir or not options.output_csv_name:
    option_parser.error('Must specify bot csv_dir and output_csv_name')

  sys.exit(CsvMerger(options.csv_dir, options.output_csv_name).Merge())

