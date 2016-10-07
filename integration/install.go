package integration

import (
	"bufio"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	. "github.com/onsi/ginkgo"

	"github.com/jmcvetta/guid"
	homedir "github.com/mitchellh/go-homedir"
	"golang.org/x/crypto/ssh"
)

var guidMaker = guid.SimpleGenerator()

func leaveIt() bool {
	return os.Getenv("LEAVE_ARTIFACTS") != ""
}
func bailBeforeAnsible() bool {
	return os.Getenv("BAIL_BEFORE_ANSIBLE") != ""
}

type NodeCount struct {
	Etcd   uint16
	Master uint16
	Worker uint16
}

func (nc NodeCount) Total() uint16 {
	return nc.Etcd + nc.Master + nc.Worker
}

func InstallKismaticMini(nodeType string, user string) PlanAWS {
	By("Building a template")
	template, err := template.New("planAWSOverlay").Parse(planAWSOverlay)
	FailIfError(err, "Couldn't parse template")

	By("Making infrastructure")
	etcdNode, etcErr := MakeWorkerNode(nodeType)
	FailIfError(etcErr, "Error making etcd node")

	defer TerminateInstances(etcdNode.Instanceid)
	descErr := WaitForInstanceToStart(&etcdNode)
	masterNode := etcdNode
	workerNode := etcdNode
	FailIfError(descErr, "Error waiting for nodes")
	log.Printf("Created etcd nodes: %v (%v), master nodes %v (%v), workerNodes %v (%v)",
		etcdNode.Instanceid, etcdNode.Publicip,
		masterNode.Instanceid, masterNode.Publicip,
		workerNode.Instanceid, workerNode.Publicip)

	By("Building a plan to set up an overlay network cluster on this hardware")
	nodes := PlanAWS{
		Etcd:                []AWSNodeDeets{etcdNode},
		Master:              []AWSNodeDeets{masterNode},
		Worker:              []AWSNodeDeets{workerNode},
		MasterNodeFQDN:      masterNode.Hostname,
		MasterNodeShortName: masterNode.Hostname,
		User:                user,
	}
	var hdErr error
	nodes.HomeDirectory, hdErr = homedir.Dir()
	FailIfError(hdErr, "Error getting home directory")

	f, fileErr := os.Create("kismatic-testing.yaml")
	FailIfError(fileErr, "Error waiting for nodes")
	defer f.Close()
	w := bufio.NewWriter(f)
	execErr := template.Execute(w, &nodes)
	FailIfError(execErr, "Error filling in plan template")
	w.Flush()

	By("Validing our plan")
	ver := exec.Command("./kismatic", "install", "validate", "-f", f.Name())
	verbytes, verErr := ver.CombinedOutput()
	verText := string(verbytes)

	FailIfError(verErr, "Error validating plan", verText)

	if bailBeforeAnsible() == true {
		return nodes
	}

	By("Punch it Chewie!")
	app := exec.Command("./kismatic", "install", "apply", "-f", f.Name())
	app.Stdout = os.Stdout
	app.Stderr = os.Stderr
	appErr := app.Run()

	FailIfError(appErr, "Error applying plan")
	return nodes
}

func InstallKismatic(nodeType string, user string) PlanAWS {
	By("Building a template")
	template, err := template.New("planAWSOverlay").Parse(planAWSOverlay)
	FailIfError(err, "Couldn't parse template")

	By("Making infrastructure")
	etcdNode, etcErr := MakeETCDNode(nodeType)
	FailIfError(etcErr, "Error making etcd node")

	masterNode, masterErr := MakeMasterNode(nodeType)
	FailIfError(masterErr, "Error making master node")

	workerNode, workerErr := MakeWorkerNode(nodeType)
	FailIfError(workerErr, "Error making worker node")

	defer TerminateInstances(etcdNode.Instanceid, masterNode.Instanceid, workerNode.Instanceid)
	descErr := WaitForInstanceToStart(&etcdNode, &masterNode, &workerNode)
	FailIfError(descErr, "Error waiting for nodes")
	log.Printf("Created etcd nodes: %v (%v), master nodes %v (%v), workerNodes %v (%v)",
		etcdNode.Instanceid, etcdNode.Publicip,
		masterNode.Instanceid, masterNode.Publicip,
		workerNode.Instanceid, workerNode.Publicip)

	By("Building a plan to set up an overlay network cluster on this hardware")
	nodes := PlanAWS{
		Etcd:                []AWSNodeDeets{etcdNode},
		Master:              []AWSNodeDeets{masterNode},
		Worker:              []AWSNodeDeets{workerNode},
		MasterNodeFQDN:      masterNode.Hostname,
		MasterNodeShortName: masterNode.Hostname,
		User:                user,
	}
	var hdErr error
	nodes.HomeDirectory, hdErr = homedir.Dir()
	FailIfError(hdErr, "Error getting home directory")

	f, fileErr := os.Create("kismatic-testing.yaml")
	FailIfError(fileErr, "Error waiting for nodes")
	defer f.Close()
	w := bufio.NewWriter(f)
	execErr := template.Execute(w, &nodes)
	FailIfError(execErr, "Error filling in plan template")
	w.Flush()

	By("Validing our plan")
	ver := exec.Command("./kismatic", "install", "validate", "-f", f.Name())
	verbytes, verErr := ver.CombinedOutput()
	verText := string(verbytes)

	FailIfError(verErr, "Error validating plan", verText)

	if bailBeforeAnsible() == true {
		return nodes
	}

	By("Punch it Chewie!")
	app := exec.Command("./kismatic", "install", "apply", "-f", f.Name())
	app.Stdout = os.Stdout
	app.Stderr = os.Stderr
	appErr := app.Run()

	FailIfError(appErr, "Error applying plan")
	return nodes
}

func InstallBigKismatic(count NodeCount, nodeType string, user string) PlanAWS {
	By("Building a template")
	template, err := template.New("planAWSOverlay").Parse(planAWSOverlay)
	FailIfError(err, "Couldn't parse template")
	if count.Etcd < 1 || count.Master < 1 || count.Worker < 1 {
		Fail("Must have at least 1 of ever node type")
	}

	By("Making infrastructure")

	allInstanceIDs := make([]string, count.Total())
	etcdNodes := make([]AWSNodeDeets, count.Etcd)
	masterNodes := make([]AWSNodeDeets, count.Master)
	workerNodes := make([]AWSNodeDeets, count.Worker)

	for i := uint16(0); i < count.Etcd; i++ {
		var etcErr error
		etcdNodes[i], etcErr = MakeETCDNode(nodeType)
		FailIfError(etcErr, "Error making etcd node")
		allInstanceIDs[i] = etcdNodes[i].Instanceid
	}

	for i := uint16(0); i < count.Master; i++ {
		var masterErr error
		masterNodes[i], masterErr = MakeMasterNode(nodeType)
		FailIfError(masterErr, "Error making master node")
		allInstanceIDs[i+count.Etcd] = masterNodes[i].Instanceid
	}

	for i := uint16(0); i < count.Worker; i++ {
		var workerErr error
		workerNodes[i], workerErr = MakeWorkerNode(nodeType)
		FailIfError(workerErr, "Error making worker node")
		allInstanceIDs[i+count.Etcd+count.Master] = workerNodes[i].Instanceid
	}

	defer TerminateInstances(allInstanceIDs...)
	nodes := PlanAWS{
		Etcd:                etcdNodes,
		Master:              masterNodes,
		Worker:              workerNodes,
		MasterNodeFQDN:      masterNodes[0].Hostname,
		MasterNodeShortName: masterNodes[0].Hostname,
		User:                user,
	}
	descErr := WaitForAllInstancesToStart(&nodes)
	FailIfError(descErr, "Error waiting for nodes")
	log.Printf("%v", nodes.Etcd[0].Publicip)
	PrintNodes(&nodes)

	By("Building a plan to set up an overlay network cluster on this hardware")
	var hdErr error
	nodes.HomeDirectory, hdErr = homedir.Dir()
	FailIfError(hdErr, "Error getting home directory")

	f, fileErr := os.Create("kismatic-testing.yaml")
	FailIfError(fileErr, "Error waiting for nodes")
	defer f.Close()
	w := bufio.NewWriter(f)
	execErr := template.Execute(w, &nodes)
	FailIfError(execErr, "Error filling in plan template")
	w.Flush()

	By("Validing our plan")
	ver := exec.Command("./kismatic", "install", "validate", "-f", f.Name())
	verbytes, verErr := ver.CombinedOutput()
	verText := string(verbytes)

	FailIfError(verErr, "Error validating plan", verText)

	if bailBeforeAnsible() == true {
		return nodes
	}

	By("Punch it Chewie!")
	app := exec.Command("./kismatic", "install", "apply", "-f", f.Name())
	app.Stdout = os.Stdout
	app.Stderr = os.Stderr
	appErr := app.Run()

	FailIfError(appErr, "Error applying plan")

	return nodes
}

func PrintNodes(plan *PlanAWS) {
	log.Printf("Created etcd nodes:")
	printNode(&plan.Etcd)
	log.Printf("Created master nodes:")
	printNode(&plan.Master)
	log.Printf("Created worker nodes:")
	printNode(&plan.Worker)
}

func printNode(aws *[]AWSNodeDeets) {
	for _, node := range *aws {
		log.Printf("\t%v (%v)", node.Instanceid, node.Publicip)
	}
}

func FailIfError(err error, message ...string) {
	if err != nil {
		log.Printf(message[0]+": %v\n%v", err, message[1:])
		Fail(message[0])
	}
}

func CopyKismaticToTemp() string {
	tmpDir := os.TempDir()
	randomness, randomErr := GenerateGUIDString()
	if randomErr != nil {
		log.Fatal("Error making a GUID: ", randomErr)
	}
	kisPath := tmpDir + "/kisint/" + randomness
	err := os.MkdirAll(kisPath, 0777)
	if err != nil {
		log.Fatal("Error making temp dir: ", err)
	}

	return kisPath
}

func GenerateGUIDString() (string, error) {
	randomness, randomErr := guidMaker.NextId()

	if randomErr != nil {
		return "", randomErr
	}

	return strconv.FormatInt(randomness, 16), nil
}

func AssertKismaticDirectory(kisPath string) {
	if FileExists(kisPath + "/kismatic") {
		log.Fatal("Installer unpacked but kismatic wasn't there")
	}
}

func FileExists(path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return true
}

func MakeETCDNode(nodeType string) (AWSNodeDeets, error) {
	return MakeAWSNode(nodeType, ec2.InstanceTypeT2Micro)
}

func MakeMasterNode(nodeType string) (AWSNodeDeets, error) {
	return MakeAWSNode(nodeType, ec2.InstanceTypeT2Micro)
}

func MakeWorkerNode(nodeType string) (AWSNodeDeets, error) {
	return MakeAWSNode(nodeType, ec2.InstanceTypeT2Medium)
}

func MakeAWSNode(ami string, instanceType string) (AWSNodeDeets, error) {
	svc := ec2.New(session.New(&aws.Config{Region: aws.String(TARGET_REGION)}))
	runResult, err := svc.RunInstances(&ec2.RunInstancesInput{
		ImageId: aws.String(ami),
		BlockDeviceMappings: []*ec2.BlockDeviceMapping{
			{
				DeviceName: aws.String("/dev/sda1"),
				Ebs: &ec2.EbsBlockDevice{
					DeleteOnTermination: aws.Bool(true),
					VolumeSize:          aws.Int64(8),
				},
			},
		},
		InstanceType:     aws.String(instanceType),
		MinCount:         aws.Int64(1),
		MaxCount:         aws.Int64(1),
		SubnetId:         aws.String(SUBNETID),
		KeyName:          aws.String(KEYNAME),
		SecurityGroupIds: []*string{aws.String(SECURITYGROUPID)},
	})

	if err != nil {
		return AWSNodeDeets{}, err
	}

	re := regexp.MustCompile("[^.]*")
	hostname := re.FindString(*runResult.Instances[0].PrivateDnsName)

	deets := AWSNodeDeets{
		Instanceid: *runResult.Instances[0].InstanceId,
		Privateip:  *runResult.Instances[0].PrivateIpAddress,
		Hostname:   hostname,
	}

	params := &ec2.ModifyInstanceAttributeInput{
		InstanceId: aws.String(deets.Instanceid), // Required
		SourceDestCheck: &ec2.AttributeBooleanValue{
			Value: aws.Bool(false),
		},
	}
	_, err2 := svc.ModifyInstanceAttribute(params)

	if err2 != nil {
		return AWSNodeDeets{}, err2
	}

	_, errtag := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String(deets.Instanceid)},
		Tags: []*ec2.Tag{
			{
				Key:   aws.String("ApprendaTeam"),
				Value: aws.String("Kismatic"),
			},
		},
	})
	if errtag != nil {
		return deets, errtag
	}

	return deets, nil
}

func TerminateInstances(instanceids ...string) {
	if leaveIt() {
		return
	}
	awsinstanceids := make([]*string, len(instanceids))
	for i, id := range instanceids {
		awsinstanceids[i] = aws.String(id)
	}
	sess, err := session.NewSession()

	if err != nil {
		log.Printf("failed to create session: %v", err)
		return
	}

	svc := ec2.New(sess, &aws.Config{Region: aws.String(TARGET_REGION)})

	params := &ec2.TerminateInstancesInput{
		InstanceIds: awsinstanceids,
	}
	resp, err := svc.TerminateInstances(params)

	if err != nil {
		log.Printf("Could not terminate: %v", resp)
		return
	}
}

func WaitForAllInstancesToStart(plan *PlanAWS) error {
	for i := 0; i < len(plan.Etcd); i++ {
		if err := WaitForInstanceToStart(&plan.Etcd[i]); err != nil {
			return err
		}
	}
	for i := 0; i < len(plan.Master); i++ {
		if err := WaitForInstanceToStart(&plan.Master[i]); err != nil {
			return err
		}
	}
	for i := 0; i < len(plan.Worker); i++ {
		if err := WaitForInstanceToStart(&plan.Worker[i]); err != nil {
			return err
		}
	}

	return nil
}

func WaitForInstanceToStart(nodes ...*AWSNodeDeets) error {
	sess, err := session.NewSession()

	if err != nil {
		fmt.Println("failed to create session,", err)
		return err
	}

	fmt.Print("Waiting for nodes to come up")
	defer fmt.Println()

	svc := ec2.New(sess, &aws.Config{Region: aws.String(TARGET_REGION)})
	for _, deets := range nodes {
		deets.Publicip = ""

		for deets.Publicip == "" {
			fmt.Print(".")
			descResult, descErr := svc.DescribeInstances(&ec2.DescribeInstancesInput{
				InstanceIds: []*string{aws.String(deets.Instanceid)},
			})
			if descErr != nil {
				return descErr
			}

			if *descResult.Reservations[0].Instances[0].State.Name == ec2.InstanceStateNameRunning &&
				descResult.Reservations[0].Instances[0].PublicIpAddress != nil {
				deets.Publicip = *descResult.Reservations[0].Instances[0].PublicIpAddress
				BlockUntilSSHOpen(deets)
			} else {
				time.Sleep(1 * time.Second)
			}
		}
	}
	return nil
}

func BlockUntilSSHOpen(node *AWSNodeDeets) {
	config := &ssh.ClientConfig{
		User: "dummyUser",
	}
	for {
		client, err := ssh.Dial("tcp", node.Publicip+":22", config)
		if err != nil && strings.Contains(err.Error(), "ssh: handshake failed") {
			// SSH server is up and running
			return
		}
		if err != nil {
			fmt.Print("?")
			time.Sleep(1 * time.Second)
			continue
		}
		client.Close()
		return
	}
}
