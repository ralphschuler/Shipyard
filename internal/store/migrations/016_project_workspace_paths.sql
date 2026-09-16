UPDATE projects SET local_path='/home/agent/.taskboard-projects/' || id::text WHERE repository_url<>'' AND local_path='';
