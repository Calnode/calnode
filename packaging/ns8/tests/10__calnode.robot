*** Settings ***
Library    SSHLibrary

*** Variables ***
${TEST_HOST}    calnode.example.test
${module_id}    ${EMPTY}

*** Test Cases ***
Check if ns8-calnode is installed correctly
    ${output}  ${rc} =    Execute Command    add-module ${IMAGE_URL} 1
    ...    return_rc=True
    Should Be Equal As Integers    ${rc}  0
    &{output} =    Evaluate    ${output}
    Set Global Variable    ${module_id}    ${output.module_id}

Check if ns8-calnode can be configured
    ${rc} =    Execute Command    api-cli run module/${module_id}/configure-module --data '{"host": "${TEST_HOST}", "lets_encrypt": false, "http2https": false}'
    ...    return_rc=True  return_stdout=False
    Should Be Equal As Integers    ${rc}  0

Check if ns8-calnode works as expected
    # Host-based route: pass the hostname explicitly since it does not resolve here
    ${rc} =    Execute Command    curl -f -H 'Host: ${TEST_HOST}' http://127.0.0.1/
    ...    return_rc=True  return_stdout=False
    Should Be Equal As Integers    ${rc}  0