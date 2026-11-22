## Unreleased — the console checks an object key before sending it

- The console holds every key it sends to the object key rule first
  (`frontend/src/lib/objects/objectKey.ts`, tested against
  `sdk/testdata/object_keys.json`): the copy and move dialog and the bulk
  copy prefix name the problem beside the field and keep the confirm button
  disabled, and an upload whose folder and file name make a refused key
  fails in the upload list without a request.
